package todoserver

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// A05 security misconfiguration: the headers on every kind of response.
func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	for _, r := range []reply{c.do("GET", "/", nil), c.do("GET", "/app.js", nil), c.do("GET", "/api/me", nil), c.do("GET", "/missing", nil)} {
		h := r.Header()
		csp := h.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("CSP lacks %s: %q", want, csp)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("CSP allows unsafe code: %q", csp)
		}
		for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
			if h.Get(k) != v {
				t.Errorf("%s = %q", k, h.Get(k))
			}
		}
		if h.Get("Strict-Transport-Security") == "" || h.Get("Permissions-Policy") == "" {
			t.Error("missing HSTS or Permissions-Policy")
		}
		if h.Get("Access-Control-Allow-Origin") != "" {
			t.Error("CORS opened")
		}
	}
	if r := c.do("GET", "/api/me", nil); r.Header().Get("Cache-Control") != "no-store" {
		t.Error("API responses may be cached")
	}
}

// Path traversal: the files served are exactly the app's, and nothing next to the data folder ever comes
// out, however the path is written.
func TestStaticFilesCannotBeEscaped(t *testing.T) {
	e := newEnv(t).withAccount()
	if err := os.Symlink(filepath.Join(e.dir, "secret.txt"), filepath.Join(e.dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/../secret.txt", "/..%2fsecret.txt", "/%2e%2e/secret.txt", "/%252e%252e/secret.txt", "/..\\secret.txt",
		"/%2e%2e%5csecret.txt", "/secret.txt", "/data/secret.txt", "/auth.json", "/todo.json", "/audit.jsonl",
		"/..%c0%afsecret.txt", "/%c0%ae%c0%ae/secret.txt", "/index.html%00.txt", "//etc/passwd",
		"/" + strings.Repeat("../", 10) + "etc/passwd", "/link.txt", "/./../data/auth.json",
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.URL.RawPath, req.URL.Path = "", ""
		req.RequestURI = p
		u, err := req.URL.Parse(p)
		if err == nil {
			req.URL = u
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if strings.Contains(body, bait) || strings.Contains(body, "root:") || strings.Contains(body, `"hash"`) {
			t.Errorf("%s served a file it must not: %d", p, rec.Code)
		}
		if rec.Code == 200 && !strings.Contains(body, "<title>app</title>") {
			t.Errorf("%s: 200 with %q", p, truncate(body, 60))
		}
	}
}

// A03 injection: what is stored comes back as data, in JSON, and the app never turns it into markup.
func TestStoredTextComesBackAsData(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	payloads := []string{
		`<script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`, `javascript:alert(1)`,
		`{{constructor.constructor('alert(1)')()}}`, `'; DROP TABLE tasks; --`, `__proto__`, "line\r\nSet-Cookie: x=1",
		`<svg/onload=alert(1)>`, "\u202e reversed", `${7*7}`,
	}
	for _, p := range payloads {
		r := c.do("POST", "/api/tasks", map[string]string{"line": p})
		if r.Code != 201 && r.Code != 400 {
			t.Errorf("%q: %d", p, r.Code)
		}
		if ct := r.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%q answered as %s", p, ct)
		}
		if r.Header().Get("Set-Cookie") != "" && strings.Contains(r.Header().Get("Set-Cookie"), "x=1") {
			t.Errorf("header injection through %q", p)
		}
	}
	r := c.do("GET", "/api/tasks", nil)
	if strings.Contains(r.Body.String(), "<script>") {
		t.Error("markup returned unescaped in JSON")
	}
}

// The web app builds its page from text, never from markup, and runs no code it was given. The
// exceptions list stays empty unless each entry says why it is safe.
func TestWebAppNeverInterpretsData(t *testing.T) {
	forbidden := regexp.MustCompile(`\.innerHTML|\.outerHTML|insertAdjacentHTML|document\.write|\beval\s*\(|new\s+Function\s*\(|setTimeout\s*\(\s*["'\x60]|setInterval\s*\(\s*["'\x60]|DOMParser|createContextualFragment|srcdoc`)
	inlineHTML := regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>\s*[^<\s]|<style|\son[a-z]+\s*=|\sstyle\s*=|javascript:`)
	files, _ := filepath.Glob("web/*")
	if len(files) == 0 {
		t.Fatal("no web app files")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		switch filepath.Ext(f) {
		case ".js":
			for _, m := range forbidden.FindAllString(string(b), -1) {
				t.Errorf("%s uses %s", f, m)
			}
			if strings.Contains(string(b), "localStorage") && regexp.MustCompile(`localStorage[^\n]*(csrf|token|password)`).Match(b) {
				t.Errorf("%s may keep a secret in localStorage", f)
			}
		case ".html":
			for _, m := range inlineHTML.FindAllString(string(b), -1) {
				t.Errorf("%s has inline code or style: %s", f, m)
			}
		}
	}
}

// RCE: the server's code never runs commands, evaluates code or builds SQL.
func TestServerCodeRunsNoCommands(t *testing.T) {
	names, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			switch strings.Trim(imp.Path.Value, `"`) {
			case "os/exec", "plugin", "database/sql", "unsafe", "reflect", "text/template", "html/template":
				t.Errorf("%s imports %s", name, imp.Path.Value)
			}
		}
	}
}

// Limits: a large body is refused before it is read, deep or odd JSON is a 400, never a 500.
func TestBodyLimits(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	big := `{"line":"` + strings.Repeat("a", maxBody) + `"}`
	c.expect(c.do("POST", "/api/tasks", big), 413, "a body over the limit")
	deep := strings.Repeat(`{"a":`, 5000) + "1" + strings.Repeat("}", 5000)
	c.expect(c.do("POST", "/api/tasks", deep), 400, "deeply nested JSON")
	c.expect(c.do("POST", "/api/tasks", `{"line":"a"}{"line":"b"}`), 400, "two JSON values")
	c.expect(c.do("POST", "/api/tasks", `{"line":"a","extra":1}`), 400, "an unknown field")
	c.expect(c.do("POST", "/api/tasks", `not json`), 400, "not JSON")
	c.expect(c.do("POST", "/api/tasks", map[string]string{"line": strings.Repeat("é", maxLine+1)}), 400, "a line over 500 characters")
	c.expect(c.do("POST", "/api/tasks", map[string]string{"line": "#Work @today"}), 400, "a task with no text")
	// JSON decoding turns invalid UTF-8 into U+FFFD: what is stored is always valid text.
	if r := c.do("POST", "/api/tasks", "{\"line\":\"\xff\xfe\"}"); r.Code == 201 && !utf8.ValidString(r.Body.String()) {
		t.Errorf("invalid UTF-8 stored: %q", r.Body)
	}
}

// Type fuzzing: every field of every write gets every JSON type; nothing may answer 500.
func TestWrongTypesNeverCrash(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	dev := c.do("POST", "/api/devices", map[string]string{"name": "Mac"}).str("token")
	id := c.do("POST", "/api/tasks", map[string]string{"line": "x"}).body["task"].(map[string]any)["id"].(string)
	fields := map[string][]string{
		"/api/login": {"password"}, "/api/login/mfa": {"code"}, "/api/tasks": {"line"}, "/api/tasks/" + id: {"line", "done"},
		"/api/password": {"current", "new"}, "/api/mfa/confirm": {"code"}, "/api/mfa/disable": {"password"},
		"/api/devices": {"name"}, "/api/sync": {"tasks"},
	}
	values := []any{nil, true, 0, -1, 1e308, "", []any{}, []any{1, "a"}, map[string]any{}, map[string]any{"__proto__": 1}, strings.Repeat("x", 1000)}
	for path, names := range fields {
		method := "POST"
		if strings.HasPrefix(path, "/api/tasks/") {
			method = "PATCH"
		}
		for _, f := range names {
			for _, v := range values {
				b, _ := json.Marshal(map[string]any{f: v})
				r := c.do(method, path, string(b), func(r *http.Request) {
					if path == "/api/sync" {
						r.Header.Set("Authorization", "Bearer "+dev)
					}
				})
				if r.Code >= 500 {
					t.Errorf("%s %s %s=%v: %d %s", method, path, f, v, r.Code, r.Body)
				}
			}
		}
		if c.do("GET", "/api/me", nil).Code != 200 {
			// Fuzzed passwords may have locked the account and ended the session: reset it as on the server.
			if err := e.s.SetPassword(password); err != nil {
				t.Fatal(err)
			}
			c.signIn()
		}
	}
}

// A09 logging: a whole session's logs and the audit log hold no password, token, code or CSRF token,
// and the events are there.
func TestLogsHoldNoSecrets(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	e := newEnv(t).withAccount()
	c := e.client()
	c.do("POST", "/api/login", map[string]string{"password": "a wrong guess here"})
	c.signIn()
	secret, codes := turnOnMFA(t, e, c)
	dev := c.do("POST", "/api/devices", map[string]string{"name": "Mac"}).str("token")
	c.do("POST", "/api/password", map[string]string{"current": password, "new": "a brand new passphrase"})
	c.do("POST", "/api/logout", "{}")

	secrets := []string{password, "a wrong guess here", "a brand new passphrase", secret, codes[0], dev, c.csrf}
	for _, ck := range c.cookies {
		secrets = append(secrets, ck.Value)
	}
	audit := readAll(t, e.s.paths.audit())
	state := readAll(t, e.s.paths.state())
	for _, s := range secrets {
		if s == "" {
			continue
		}
		for where, text := range map[string]string{"server log": logs.String(), "audit log": audit, "auth.json": state} {
			if strings.Contains(text, s) {
				t.Errorf("%s holds a secret (%s…)", where, truncate(s, 6))
			}
		}
	}
	for _, ev := range []string{"login_fail", "login_ok", "mfa_on", "device_added", "password_changed", "logout"} {
		if !strings.Contains(audit, `"event":"`+ev+`"`) {
			t.Errorf("audit log lacks %s", ev)
		}
	}
	if !strings.Contains(audit, `"ip":"198.51.100.7"`) {
		t.Error("audit log lacks the client IP")
	}
}

// Errors say what went wrong and nothing about the server's insides.
func TestErrorsRevealNothing(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	for _, r := range []reply{
		c.do("POST", "/api/tasks", `{`), c.do("PATCH", "/api/tasks/nope", `{"done":true}`), c.do("GET", "/nope", nil),
		c.do("DELETE", "/api/devices/nope", "{}"),
	} {
		body := r.Body.String()
		for _, leak := range []string{"goroutine", ".go:", e.dir, "/Users/", "panic", "runtime."} {
			if strings.Contains(body, leak) {
				t.Errorf("error body leaks %q: %s", leak, body)
			}
		}
	}
}

// Found by the ZAP baseline: forms without method="post" put the password in the URL (history, logs) when
// submitted before the script runs. Every form posts, so a submit without the script sends nothing in
// the URL (and the server answers 405).
func TestFormsNeverPutFieldsInTheURL(t *testing.T) {
	b, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	forms := regexp.MustCompile(`(?is)<form\b[^>]*>`).FindAllString(string(b), -1)
	if len(forms) == 0 {
		t.Fatal("no forms found")
	}
	for _, f := range forms {
		if !regexp.MustCompile(`(?i)\bmethod\s*=\s*"post"`).MatchString(f) {
			t.Errorf("form without method=\"post\": %s", f)
		}
	}
	js, _ := os.ReadFile("web/app.js")
	if regexp.MustCompile(`h\(\s*'form'`).Match(js) && !regexp.MustCompile(`h\(\s*'form'\s*,\s*\{[^}]*method:\s*'post'`).Match(js) {
		t.Error("app.js builds a form without method post")
	}
}
