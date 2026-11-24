package todoserver

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// A01 broken access control. Every route in the table is checked: signed-out, with the wrong
// credentials, with the other kind of credentials, and against the documentation.

func samplePath(p string) string { return strings.ReplaceAll(p, "{id}", "nope") }

func TestEveryRouteRefusesStrangers(t *testing.T) {
	e := newEnv(t).withAccount()
	stranger := e.client()
	for _, rt := range routes {
		r := stranger.do(rt.method, samplePath(rt.path), "{}")
		switch rt.access {
		case public:
			// Passkey sign-in options answer anyone: a single-use random challenge, nothing about the
			// account, limited per IP and capped in number (TestPasskeyChallengesAreCapped).
			if rt.path == "/api/login/passkey/options" {
				continue
			}
			if r.Code == 200 || r.Code == 201 || r.Code == 204 {
				t.Errorf("%s %s answered %d to an empty request", rt.method, rt.path, r.Code)
			}
		case signed, machine:
			stranger.expect(r, 401, rt.method+" "+rt.path+" signed out")
		}
	}
}

func TestDeviceTokenOpensOnlySync(t *testing.T) {
	e := newEnv(t).withAccount()
	b := e.client()
	b.signIn()
	token := b.do("POST", "/api/devices", map[string]string{"name": "Mac"}).str("token")
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	m := e.client()
	for _, rt := range routes {
		if rt.access != signed {
			continue
		}
		m.expect(m.do(rt.method, samplePath(rt.path), "{}", bearer(token)), 401, rt.method+" "+rt.path+" with a device token")
	}
	m.expect(m.do("POST", "/api/sync", `{"tasks":[]}`, bearer(token)), 200, "sync with the device token")
	m.expect(m.do("POST", "/api/sync", `{"tasks":[]}`, bearer(token+"x")), 401, "sync with a wrong token")
	m.expect(m.do("POST", "/api/sync", `{"tasks":[]}`, bearer("")), 401, "sync with an empty token")
	// A session cookie does not open sync either.
	b.expect(b.do("POST", "/api/sync", `{"tasks":[]}`), 401, "sync with a session cookie")
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	e := newEnv(t).withAccount()
	b := e.client()
	b.signIn()
	b.expect(b.do("GET", "/api/nothing", nil), 404, "unknown API path")
	b.expect(b.do("GET", "/api/login", nil), 404, "GET on a POST-only route") // falls to the files, which have none
	b.expect(b.do("PUT", "/api/tasks", "{}"), 405, "PUT on tasks")
	b.expect(b.do("DELETE", "/api/me", "{}"), 405, "DELETE on me")
}

// The route table and docs/todo-server.md describe the same API: a route added to one and not the
// other fails here.
func TestRoutesMatchTheDocs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/todo-server.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`(GET|POST|PATCH|DELETE) (/api/[^ `]+)").FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]+" "+m[2]] = true
	}
	inCode := map[string]bool{}
	for _, rt := range routes {
		k := rt.method + " " + rt.path
		inCode[k] = true
		if !documented[k] {
			t.Errorf("route %s is not in docs/todo-server.md", k)
		}
	}
	for k := range documented {
		if !inCode[k] {
			t.Errorf("docs/todo-server.md lists %s, which does not exist", k)
		}
	}
}
