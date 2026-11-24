// Package todoserver is cockpit-todo-server: a copy of the to-do list on a server of one's own, with a
// web app for the phone and a sync endpoint for the computers. docs/todo-server.md describes it.
package todoserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Limits of the sign-in and the session (login-seguro, ASVS level 2).
const (
	lockAfter       = 5                // wrong attempts before the account locks
	lockBase        = 15 * time.Minute // first lock; each next one doubles
	lockMax         = 24 * time.Hour
	loginPerIP      = 30 // attempts per IP per loginWindow, right or wrong
	loginWindow     = 15 * time.Minute
	sessionIdle     = 30 * time.Minute
	sessionMax      = 12 * time.Hour
	challengeTTL    = 5 * time.Minute
	touchEvery      = time.Minute // how often a session's last use is written down
	endedKeep       = 30 * 24 * time.Hour
	maxBody         = 64 << 10
	maxSyncBody     = 4 << 20
	sessionCookie   = "__Host-todo"
	challengeCookie = "__Host-todo-mfa"
	csrfHeader      = "X-CSRF-Token"
	appName         = "Agent Cockpit"
)

// Config is how the server runs; cmd/cockpit-todo-server fills it from the environment.
type Config struct {
	Data         string          // folder holding todo.json, auth.json and audit.jsonl
	Origin       string          // the public origin, https://todo.example.com
	EncKey       []byte          // seals the 2FA secret; nil turns 2FA off
	Trust        auth.TrustProxy // who may tell the client's IP
	DefaultGroup string          // the group of a task without one, as the web app shows it
	Static       fs.FS           // the web app
	Now          func() time.Time
}

// Server holds the account state in memory, saving it on every change. One process serves one list.
type Server struct {
	cfg   Config
	paths dataPaths
	tasks *todo.Store
	login auth.Limiter

	mu sync.Mutex
	st state
}

// New opens the data folder.
func New(cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DefaultGroup == "" {
		cfg.DefaultGroup = "Tasks"
	}
	if !strings.HasPrefix(cfg.Origin, "https://") && !strings.HasPrefix(cfg.Origin, "http://localhost") && !strings.HasPrefix(cfg.Origin, "http://127.0.0.1") {
		return nil, fmt.Errorf("TODO_ORIGIN must be the https:// origin the app is served from, got %q", cfg.Origin)
	}
	cfg.Origin = strings.TrimRight(cfg.Origin, "/")
	if err := os.MkdirAll(cfg.Data, 0o700); err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, paths: dataPaths{cfg.Data}}
	s.login.Now = cfg.Now
	s.tasks = &todo.Store{Path: s.paths.todo()}
	st, err := loadState(s.paths.state())
	if err != nil {
		return nil, err
	}
	s.st = st
	return s, nil
}

// access says who may call a route.
type access int

const (
	public  access = iota // anyone: the web app's files, signing in
	signed                // a session
	machine               // a device token
)

type route struct {
	method, path string
	access       access
	handle       func(*Server, *call) (any, error)
}

// routes is every API endpoint. The security tests walk this table: a route missing from it does not
// exist, and each one's access is checked.
var routes = []route{
	{"POST", "/api/login", public, (*Server).postLogin},
	{"POST", "/api/login/mfa", public, (*Server).postLoginMFA},
	{"POST", "/api/logout", signed, (*Server).postLogout},
	{"GET", "/api/me", signed, (*Server).getMe},
	{"GET", "/api/tasks", signed, (*Server).getTasks},
	{"POST", "/api/tasks", signed, (*Server).postTask},
	{"PATCH", "/api/tasks/{id}", signed, (*Server).patchTask},
	{"DELETE", "/api/tasks/{id}", signed, (*Server).deleteTask},
	{"POST", "/api/password", signed, (*Server).postPassword},
	{"GET", "/api/sessions", signed, (*Server).getSessions},
	{"DELETE", "/api/sessions/{id}", signed, (*Server).deleteSession},
	{"POST", "/api/sessions/end-others", signed, (*Server).postEndOthers},
	{"POST", "/api/mfa/start", signed, (*Server).postMFAStart},
	{"POST", "/api/mfa/confirm", signed, (*Server).postMFAConfirm},
	{"POST", "/api/mfa/disable", signed, (*Server).postMFADisable},
	{"GET", "/api/devices", signed, (*Server).getDevices},
	{"POST", "/api/devices", signed, (*Server).postDevice},
	{"DELETE", "/api/devices/{id}", signed, (*Server).deleteDevice},
	{"GET", "/api/audit", signed, (*Server).getAudit},
	{"POST", "/api/sync", machine, (*Server).postSync},
}

// call is one API request as a handler sees it.
type call struct {
	w       http.ResponseWriter
	r       *http.Request
	ip      string
	now     time.Time
	token   string // the session cookie's token, when signed in
	session *session
	device  *device
}

// httpError is an error with the status and the text the client sees.
type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func fail(status int, msg string) error { return httpError{status, msg} }

var (
	errSignedOut = fail(http.StatusUnauthorized, "Sign in first.")
	errNotFound  = fail(http.StatusNotFound, "Not found.")
)

// noContent is a handler result answered with 204.
type noContent struct{}

// Handler is the server's HTTP handler: the API and the web app, with the security headers on all of it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.path, func(w http.ResponseWriter, r *http.Request) { s.serveAPI(rt, w, r) })
	}
	if s.cfg.Static != nil {
		mux.Handle("/", s.static())
	}
	return headers(mux)
}

func (s *Server) serveAPI(rt route, w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("panic in %s %s: %v", rt.method, rt.path, v)
			writeError(w, fail(http.StatusInternalServerError, "Something went wrong."))
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	c := &call{w: w, r: r, ip: auth.ClientIP(r, s.cfg.Trust), now: s.cfg.Now()}
	limit := int64(maxBody)
	if rt.access == machine {
		limit = maxSyncBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := s.admit(rt, c); err != nil {
		writeError(w, err)
		return
	}
	out, err := rt.handle(s, c)
	switch {
	case err != nil:
		writeError(w, err)
	case out == nil:
		writeError(w, fail(http.StatusInternalServerError, "Something went wrong."))
	case out == (noContent{}):
		w.WriteHeader(http.StatusNoContent)
	default:
		status := http.StatusOK
		if r.Method == "POST" && (rt.path == "/api/tasks" || rt.path == "/api/devices") {
			status = http.StatusCreated
		}
		writeJSON(w, status, out)
	}
}

// admit applies a route's access rule before its handler runs: the session or device, and for writes
// made with a session, the content type, the origin and the CSRF token.
func (s *Server) admit(rt route, c *call) error {
	write := c.r.Method != "GET" && c.r.Method != "HEAD"
	if write && rt.access != machine {
		if mt, _, _ := mime.ParseMediaType(c.r.Header.Get("Content-Type")); mt != "application/json" {
			return fail(http.StatusUnsupportedMediaType, "Send JSON.")
		}
		if c.r.Header.Get("Origin") != s.cfg.Origin {
			return fail(http.StatusForbidden, "Requests must come from the app.")
		}
	}
	switch rt.access {
	case signed:
		ck, err := c.r.Cookie(sessionCookie)
		if err != nil {
			return errSignedOut
		}
		sess := s.resolve(ck.Value, c.now)
		if sess == nil {
			return errSignedOut
		}
		c.token, c.session = ck.Value, sess
		if write && !auth.Equal(c.r.Header.Get(csrfHeader), csrfFor(c.token)) {
			return fail(http.StatusForbidden, "The page is out of date. Reload it.")
		}
	case machine:
		tok, ok := strings.CutPrefix(c.r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			return fail(http.StatusUnauthorized, "Send the device token.")
		}
		d := s.deviceFor(tok, c.now)
		if d == nil {
			return fail(http.StatusUnauthorized, "Unknown device token. Connect this computer again.")
		}
		c.device = d
	}
	return nil
}

func csrfFor(token string) string { return auth.MAC(token, "cockpit-todo-csrf") }

// decode reads a JSON body into v. Wrong types, unknown fields, a second value and an oversized body are
// the client's mistake, never a 500.
func decode(c *call, v any) error {
	dec := json.NewDecoder(c.r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fail(http.StatusRequestEntityTooLarge, "Too much data.")
		}
		return fail(http.StatusBadRequest, "That request is not valid.")
	}
	if dec.More() {
		return fail(http.StatusBadRequest, "That request is not valid.")
	}
	if _, err := io.Copy(io.Discard, c.r.Body); err != nil {
		return fail(http.StatusRequestEntityTooLarge, "Too much data.")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var he httpError
	if !errors.As(err, &he) {
		log.Printf("error: %v", err)
		he = httpError{http.StatusInternalServerError, "Something went wrong."}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, he.status, map[string]string{"error": he.msg})
}

// headers sets the security headers on every response.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; manifest-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// static serves the web app's files: exactly the files in the embedded folder, and index.html for /.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.cfg.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			writeError(w, fail(http.StatusMethodNotAllowed, "Method not allowed."))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || strings.Contains(name, "\\") {
			writeError(w, errNotFound)
			return
		}
		fi, err := fs.Stat(s.cfg.Static, name)
		if err != nil || fi.IsDir() {
			writeError(w, errNotFound)
			return
		}
		if name == "index.html" || name == "sw.js" || name == "manifest.webmanifest" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if name == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}

// audit appends an event to the audit log. A failure is logged and does not undo the change: the change
// already happened, and refusing service over a full disk would lock the person out.
func (s *Server) audit(c *call, name string, details map[string]string) {
	e := event{At: c.now, Event: name, IP: c.ip, Agent: truncate(c.r.UserAgent(), 200), Details: details}
	if err := appendEvent(s.paths.audit(), e); err != nil {
		log.Printf("audit log: %v", err)
	}
}

// AuditCLI records an event done on the server's command line.
func (s *Server) AuditCLI(name string, details map[string]string) error {
	return appendEvent(s.paths.audit(), event{At: s.cfg.Now(), Event: name, IP: "server", Agent: "cockpit-todo-server", Details: details})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
