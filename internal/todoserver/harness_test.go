package todoserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth"
)

func init() { auth.SetCostForTests(10) }

const (
	origin   = "https://todo.test"
	password = "plum river sixteen lanterns"
	bait     = "BAIT-FILE-CONTENT-must-never-be-served"
)

// env is a server on a fake clock with a planted bait file next to its data.
type env struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	now time.Time
	dir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte(bait), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), dir: data}
	key, _ := auth.ParseKey(strings.Repeat("ab", 32))
	s, err := New(Config{
		Data: data, Origin: origin, EncKey: key, Trust: auth.ProxyCaddy, DefaultGroup: "Personal",
		Static: fstest.MapFS{
			"index.html": {Data: []byte("<!doctype html><title>app</title>")},
			"app.js":     {Data: []byte("// app")},
		},
		Now: func() time.Time { return e.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "secret.txt"), []byte(bait), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s, e.h = s, s.Handler()
	return e
}

func (e *env) withAccount() *env {
	e.t.Helper()
	if err := e.s.SetPassword(password); err != nil {
		e.t.Fatal(err)
	}
	return e
}

// client is a browser: it keeps cookies and the CSRF token, and sends the headers the app sends.
type client struct {
	e       *env
	ip      string
	cookies map[string]*http.Cookie
	csrf    string
	origin  string
}

func (e *env) client() *client {
	return &client{e: e, ip: "198.51.100.7", cookies: map[string]*http.Cookie{}, origin: origin}
}

type reply struct {
	*httptest.ResponseRecorder
	body map[string]any
}

func (r reply) str(key string) string { s, _ := r.body[key].(string); return s }

// do sends a request. body may be a string (sent as is) or a value (sent as JSON).
func (c *client) do(method, path string, body any, mods ...func(*http.Request)) reply {
	c.e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "127.0.0.1:41000" // Caddy on the same machine
	req.Header.Set("X-Forwarded-For", c.ip)
	req.Header.Set("User-Agent", "test-browser")
	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", c.origin)
		if c.csrf != "" {
			req.Header.Set(csrfHeader, c.csrf)
		}
	}
	for _, ck := range c.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	for _, m := range mods {
		m(req)
	}
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	r := reply{ResponseRecorder: rec, body: map[string]any{}}
	_ = json.Unmarshal(rec.Body.Bytes(), &r.body)
	if v, ok := r.body["csrf"].(string); ok {
		c.csrf = v
	}
	return r
}

// signIn signs in with the password (and code, when 2FA is on) and fails the test otherwise.
func (c *client) signIn(code ...string) {
	c.e.t.Helper()
	r := c.do("POST", "/api/login", map[string]string{"password": password})
	if r.Code != 200 {
		c.e.t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	if r.str("next") == "mfa" {
		if len(code) == 0 {
			c.e.t.Fatal("2FA asked and no code given")
		}
		if r = c.do("POST", "/api/login/mfa", map[string]string{"code": code[0]}); r.Code != 200 {
			c.e.t.Fatalf("login code: %d %s", r.Code, r.Body)
		}
	}
}

func (c *client) expect(r reply, status int, what string) {
	c.e.t.Helper()
	if r.Code != status {
		c.e.t.Errorf("%s: %d %s, want %d", what, r.Code, strings.TrimSpace(r.Body.String()), status)
	}
}

// totpNow is the current 2FA code for a secret as the setup screen shows it.
func (e *env) totpNow(secretB32 string, offset int64) string {
	key, err := auth.B32.DecodeString(secretB32)
	if err != nil {
		e.t.Fatal(err)
	}
	return auth.HOTP(key, uint64(auth.StepAt(e.now)+offset))
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}
