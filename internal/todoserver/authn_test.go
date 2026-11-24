package todoserver

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A07 identification and authentication failures: the login-seguro checklist (item 13).

func TestNoAccountNoSignIn(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.expect(c.do("POST", "/api/login", map[string]string{"password": password}), 401, "login before an account exists")
	c.expect(c.do("POST", "/api/login", map[string]string{"password": ""}), 401, "empty password")
}

func TestProgressiveLockout(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	wrong := map[string]string{"password": "not the password at all"}
	for i := range lockAfter {
		r := c.do("POST", "/api/login", wrong)
		c.expect(r, 401, "wrong password")
		if strings.Contains(strings.ToLower(r.Body.String()), "lock") {
			t.Fatalf("attempt %d: a wrong password learned about the lock: %s", i+1, r.Body)
		}
	}
	c.expect(c.do("POST", "/api/login", wrong), 401, "wrong password while locked")
	r := c.do("POST", "/api/login", map[string]string{"password": password})
	c.expect(r, 423, "right password while locked")
	if !strings.Contains(r.str("error"), "locked until") {
		t.Errorf("lock message: %s", r.Body)
	}

	e.now = e.now.Add(lockBase + time.Second)
	c.signIn() // the lock expired; signing in clears the count

	// A second lock in a row lasts twice as long.
	for range lockAfter {
		c.do("POST", "/api/login", wrong)
	}
	e.now = e.now.Add(lockBase + time.Second)
	if r := c.do("POST", "/api/login", map[string]string{"password": password}); r.Code != 200 {
		// level reset by the successful sign-in: the next lock is the first again
		t.Errorf("lock after a successful sign-in did not start over: %d", r.Code)
	}
}

func TestLockDoublesWithoutASuccessInBetween(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	wrong := map[string]string{"password": "not the password at all"}
	lockOnce := func() {
		for range lockAfter {
			c.do("POST", "/api/login", wrong)
		}
	}
	lockOnce()
	e.now = e.now.Add(lockBase + time.Second)
	lockOnce()
	e.now = e.now.Add(lockBase + time.Second) // the second lock is 2×lockBase
	c.expect(c.do("POST", "/api/login", map[string]string{"password": password}), 423, "second lock after one base period")
	e.now = e.now.Add(lockBase)
	c.expect(c.do("POST", "/api/login", map[string]string{"password": password}), 200, "second lock after two base periods")
}

func TestLoginLimitPerIPCannotBeDodged(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	for range loginPerIP {
		c.do("POST", "/api/login", map[string]string{"password": "x"})
	}
	c.expect(c.do("POST", "/api/login", map[string]string{"password": password}), 429, "attempts over the per-IP limit")

	// From a remote peer, X-Forwarded-For is the attacker's own text and changes nothing.
	remote := e.client()
	for i := range loginPerIP + 5 {
		r := remote.do("POST", "/api/login", map[string]string{"password": "x"}, func(r *http.Request) {
			r.RemoteAddr = "203.0.113.50:9999"
			r.Header.Set("X-Forwarded-For", "10.0.0."+string(rune('1'+i%9)))
		})
		if i >= loginPerIP && r.Code != 429 {
			t.Fatalf("attempt %d with a forged X-Forwarded-For: %d", i+1, r.Code)
		}
	}
	// Another real IP behind the proxy is not held by this IP's limit. (The wrong passwords did lock
	// the account itself, which is the per-account lock doing its job.)
	other := e.client()
	other.ip = "198.51.100.99"
	other.expect(other.do("POST", "/api/login", map[string]string{"password": password}), 423, "another IP")
}

func TestSessionCookieAndRotation(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	r := c.do("POST", "/api/login", map[string]string{"password": password})
	ck := c.cookies[sessionCookie]
	if ck == nil {
		t.Fatal("no session cookie")
	}
	raw := r.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-todo=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(raw, want) {
			t.Errorf("cookie lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(strings.ToLower(raw), "domain=") {
		t.Errorf("cookie has a Domain: %s", raw)
	}
	first := ck.Value
	c.signIn()
	if c.cookies[sessionCookie].Value == first {
		t.Error("signing in again kept the same token")
	}
	if strings.Contains(readAll(t, e.s.paths.state()), first) {
		t.Error("the session token is stored as is")
	}
}

func TestSessionExpires(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	e.now = e.now.Add(sessionIdle - time.Minute)
	c.expect(c.do("GET", "/api/me", nil), 200, "used within the idle time")
	e.now = e.now.Add(sessionIdle + time.Second)
	c.expect(c.do("GET", "/api/me", nil), 401, "idle too long")

	c.signIn()
	for e.now.Add(sessionIdle/2).Before(e.now.Add(sessionMax)) && c.do("GET", "/api/me", nil).Code == 200 {
		e.now = e.now.Add(sessionIdle / 2) // kept busy
	}
	c.expect(c.do("GET", "/api/me", nil), 401, "busy past the absolute limit")
}

func TestLogoutEndsOnlyThisSession(t *testing.T) {
	e := newEnv(t).withAccount()
	phone, laptop := e.client(), e.client()
	phone.signIn()
	laptop.signIn()
	old := phone.cookies[sessionCookie].Value
	phone.expect(phone.do("POST", "/api/logout", "{}"), 204, "logout")
	phone.cookies[sessionCookie] = &http.Cookie{Name: sessionCookie, Value: old}
	phone.expect(phone.do("GET", "/api/me", nil), 401, "the token after logout")
	laptop.expect(laptop.do("GET", "/api/me", nil), 200, "another session after logout")
}

func TestCSRF(t *testing.T) {
	e := newEnv(t).withAccount()
	a, b := e.client(), e.client()
	a.signIn()
	b.signIn()
	add := map[string]string{"line": "x"}
	good := a.csrf

	a.csrf = ""
	a.expect(a.do("POST", "/api/tasks", add), 403, "no CSRF token")
	a.csrf = "forged"
	a.expect(a.do("POST", "/api/tasks", add), 403, "forged CSRF token")
	a.csrf = b.csrf
	a.expect(a.do("POST", "/api/tasks", add), 403, "another session's CSRF token")
	a.csrf = good
	a.origin = "https://evil.test"
	a.expect(a.do("POST", "/api/tasks", add), 403, "another origin")
	a.origin = origin
	a.expect(a.do("POST", "/api/tasks", add, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }), 415, "a form post")
	a.expect(a.do("POST", "/api/tasks", add), 201, "all right")
	a.expect(a.do("POST", "/api/login", map[string]string{"password": password}, func(r *http.Request) { r.Header.Del("Origin") }), 403, "login without an origin")
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t).withAccount()
	me, other := e.client(), e.client()
	me.signIn()
	other.signIn()
	before := me.cookies[sessionCookie].Value

	me.expect(me.do("POST", "/api/password", map[string]string{"current": "wrong wrong wrong", "new": "a brand new passphrase"}), 403, "wrong current password")
	me.expect(me.do("POST", "/api/password", map[string]string{"current": password, "new": "qwerty123456"}), 400, "a common password")
	me.expect(me.do("POST", "/api/password", map[string]string{"current": password, "new": password}), 400, "the same password")
	me.expect(me.do("POST", "/api/password", map[string]string{"current": password, "new": "a brand new passphrase"}), 200, "change")

	other.expect(other.do("GET", "/api/me", nil), 401, "another session after the change")
	if me.cookies[sessionCookie].Value == before {
		t.Error("the session was not rotated")
	}
	me.expect(me.do("GET", "/api/me", nil), 200, "this session after the change")
	me.expect(me.do("POST", "/api/tasks", map[string]string{"line": "x"}), 201, "a write with the new CSRF token")

	fresh := e.client()
	fresh.expect(fresh.do("POST", "/api/login", map[string]string{"password": password}), 401, "the old password")
	fresh.expect(fresh.do("POST", "/api/login", map[string]string{"password": "a brand new passphrase"}), 200, "the new password")
}

func TestWrongCurrentPasswordCountsAndLockEndsSessions(t *testing.T) {
	e := newEnv(t).withAccount()
	me, other := e.client(), e.client()
	me.signIn()
	other.signIn()
	for range lockAfter - 1 {
		me.do("POST", "/api/password", map[string]string{"current": "wrong wrong wrong", "new": "a brand new passphrase"})
	}
	me.expect(me.do("POST", "/api/password", map[string]string{"current": "wrong wrong wrong", "new": "a brand new passphrase"}), 423, "the attempt that locks")
	other.expect(other.do("GET", "/api/me", nil), 401, "other sessions after the lock")
}

// turnOnMFA turns 2FA on for a signed-in client and returns the secret and the recovery codes.
func turnOnMFA(t *testing.T, e *env, c *client) (string, []string) {
	t.Helper()
	r := c.do("POST", "/api/mfa/start", "{}")
	secret := r.str("secret")
	if secret == "" || !strings.HasPrefix(r.str("uri"), "otpauth://totp/") {
		t.Fatalf("start: %s", r.Body)
	}
	c.expect(c.do("POST", "/api/mfa/confirm", map[string]string{"code": "000000"}), 400, "a wrong first code")
	r = c.do("POST", "/api/mfa/confirm", map[string]string{"code": e.totpNow(secret, 0)})
	var codes []string
	for _, v := range r.body["recoveryCodes"].([]any) {
		codes = append(codes, v.(string))
	}
	if len(codes) != 10 {
		t.Fatalf("recovery codes: %v", codes)
	}
	e.now = e.now.Add(31 * time.Second) // the confirming code's step is used up
	return secret, codes
}

func TestTwoStepVerification(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	secret, codes := turnOnMFA(t, e, c)
	if me := c.do("GET", "/api/me", nil); me.body["mfa"].(map[string]any)["on"] != true {
		t.Errorf("me after turning on: %s", me.Body)
	}
	if st := readAll(t, e.s.paths.state()); strings.Contains(st, secret) || strings.Contains(st, codes[0]) {
		t.Error("the 2FA secret or a recovery code is stored as is")
	}

	p := e.client()
	r := p.do("POST", "/api/login", map[string]string{"password": password})
	if r.str("next") != "mfa" || p.cookies[sessionCookie] != nil {
		t.Fatalf("password alone opened a session: %s", r.Body)
	}
	p.expect(p.do("GET", "/api/me", nil), 401, "between password and code")
	code := e.totpNow(secret, 0)
	p.expect(p.do("POST", "/api/login/mfa", map[string]string{"code": code}), 200, "the code")

	// The same code never works twice.
	q := e.client()
	q.do("POST", "/api/login", map[string]string{"password": password})
	q.expect(q.do("POST", "/api/login/mfa", map[string]string{"code": code}), 401, "a replayed code")

	// A recovery code works once.
	q.expect(q.do("POST", "/api/login/mfa", map[string]string{"code": strings.ToUpper(codes[3])}), 200, "a recovery code")
	z := e.client()
	z.do("POST", "/api/login", map[string]string{"password": password})
	z.expect(z.do("POST", "/api/login/mfa", map[string]string{"code": codes[3]}), 401, "a recovery code used twice")

	// The step between password and code lasts five minutes.
	w := e.client()
	w.do("POST", "/api/login", map[string]string{"password": password})
	e.now = e.now.Add(challengeTTL + time.Second)
	w.expect(w.do("POST", "/api/login/mfa", map[string]string{"code": e.totpNow(secret, 0)}), 401, "a code after the challenge expired")

	// No code without the password step.
	n := e.client()
	n.expect(n.do("POST", "/api/login/mfa", map[string]string{"code": e.totpNow(secret, 0)}), 401, "a code with no challenge")
}

func TestWrongCodesLock(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	turnOnMFA(t, e, c) // its wrong first code counted once
	p := e.client()
	p.do("POST", "/api/login", map[string]string{"password": password})
	var r reply
	for range lockAfter {
		r = p.do("POST", "/api/login/mfa", map[string]string{"code": "123456"})
		if r.Code == 423 {
			break
		}
	}
	p.expect(r, 423, "wrong codes until the lock")
}

func TestTurningMFAOff(t *testing.T) {
	e := newEnv(t).withAccount()
	c, other := e.client(), e.client()
	c.signIn()
	secret, _ := turnOnMFA(t, e, c)
	other.signIn(e.totpNow(secret, 0))
	c.expect(c.do("POST", "/api/mfa/disable", map[string]string{"password": "wrong wrong wrong"}), 403, "turning off with a wrong password")
	c.expect(c.do("POST", "/api/mfa/disable", map[string]string{"password": password}), 204, "turning off")
	c.expect(c.do("GET", "/api/me", nil), 401, "this session after turning off")
	other.expect(other.do("GET", "/api/me", nil), 401, "other sessions after turning off")
	n := e.client()
	if r := n.do("POST", "/api/login", map[string]string{"password": password}); r.str("next") != "done" {
		t.Errorf("2FA still asked: %s", r.Body)
	}
}

func TestSessionsList(t *testing.T) {
	e := newEnv(t).withAccount()
	me, other := e.client(), e.client()
	me.signIn()
	other.signIn()
	r := me.do("GET", "/api/sessions", nil)
	list := r.body["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions: %s", r.Body)
	}
	var otherID string
	for _, s := range list {
		m := s.(map[string]any)
		if m["current"] != true {
			otherID = m["id"].(string)
		}
		if strings.Contains(r.Body.String(), me.cookies[sessionCookie].Value) {
			t.Error("the list shows a token")
		}
	}
	me.expect(me.do("DELETE", "/api/sessions/"+otherID, "{}"), 204, "ending the other session")
	other.expect(other.do("GET", "/api/me", nil), 401, "the ended session")
	other.signIn()
	if r := me.do("POST", "/api/sessions/end-others", "{}"); r.body["ended"] != float64(1) {
		t.Errorf("end-others: %s", r.Body)
	}
	other.expect(other.do("GET", "/api/me", nil), 401, "after end-others")
	me.expect(me.do("GET", "/api/me", nil), 200, "this session after end-others")
}

func TestResetOnTheServerEndsSessions(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	if err := e.s.SetPassword("another long passphrase"); err != nil {
		t.Fatal(err)
	}
	c.expect(c.do("GET", "/api/me", nil), 401, "a session after the reset")
	if err := e.s.SetPassword("short"); err == nil {
		t.Error("the server set a weak password")
	}
}

// Found by staticcheck: the lock check read the account before knowing there was one.
func TestLockCheckWithoutAccount(t *testing.T) {
	e := newEnv(t)
	if _, locked := e.s.lockedUntil(e.now); locked {
		t.Error("no account reads as locked")
	}
}
