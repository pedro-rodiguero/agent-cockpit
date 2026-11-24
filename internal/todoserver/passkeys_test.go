package todoserver

import (
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth/authtest"
)

const rp = "todo.test"

func challengeOf(t *testing.T, r reply) string {
	t.Helper()
	pk, _ := r.body["publicKey"].(map[string]any)
	ch, _ := pk["challenge"].(string)
	if ch == "" {
		t.Fatalf("no challenge: %s", r.Body)
	}
	return ch
}

// addPasskey registers a software passkey for a signed-in client.
func addPasskey(t *testing.T, c *client, k *authtest.Key) {
	t.Helper()
	ch := challengeOf(t, c.do("POST", "/api/passkeys/options", map[string]string{"password": password}))
	cd, att := k.Create(ch, rp, origin, authtest.UP|authtest.UV)
	r := c.do("POST", "/api/passkeys", map[string]any{"name": "iPhone", "id": b64u.EncodeToString(k.ID),
		"clientDataJSON": b64u.EncodeToString(cd), "attestationObject": b64u.EncodeToString(att), "transports": []string{"internal"}})
	c.expect(r, 201, "add a passkey")
}

// passkeySignIn signs in with k and returns the reply.
func passkeySignIn(t *testing.T, c *client, k *authtest.Key, flags byte) reply {
	t.Helper()
	ch := challengeOf(t, c.do("POST", "/api/login/passkey/options", "{}"))
	cd, ad, sig := k.Get(ch, rp, origin, flags)
	return c.do("POST", "/api/login/passkey", map[string]string{"id": b64u.EncodeToString(k.ID),
		"clientDataJSON": b64u.EncodeToString(cd), "authenticatorData": b64u.EncodeToString(ad), "signature": b64u.EncodeToString(sig)})
}

func TestPasskeySignIn(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	k := authtest.NewKey()
	c.expect(c.do("POST", "/api/passkeys/options", map[string]string{"password": "wrong wrong wrong"}), 403, "options with a wrong password")
	addPasskey(t, c, k)
	if list := c.do("GET", "/api/passkeys", nil).Body.String(); !strings.Contains(list, "iPhone") || strings.Contains(list, "publicKey") {
		t.Errorf("passkey list: %s", list)
	}

	phone := e.client()
	phone.expect(passkeySignIn(t, phone, k, authtest.UP|authtest.UV), 200, "sign in with the passkey")
	phone.expect(phone.do("GET", "/api/me", nil), 200, "the passkey's session")
	if !strings.Contains(readAll(t, e.s.paths.audit()), `"with":"passkey"`) {
		t.Error("passkey sign-in not audited")
	}

	// Without Face ID (user verification) it does not count.
	other := e.client()
	other.expect(passkeySignIn(t, other, k, authtest.UP), 401, "a passkey without user verification")
	// A key that was never registered.
	other.expect(passkeySignIn(t, other, authtest.NewKey(), authtest.UP|authtest.UV), 401, "an unknown passkey")
}

func TestPasskeyChallengeIsSingleUseAndForItsCeremony(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	k := authtest.NewKey()
	addPasskey(t, c, k)

	p := e.client()
	ch := challengeOf(t, p.do("POST", "/api/login/passkey/options", "{}"))
	cd, ad, sig := k.Get(ch, rp, origin, authtest.UP|authtest.UV)
	body := map[string]string{"id": b64u.EncodeToString(k.ID), "clientDataJSON": b64u.EncodeToString(cd),
		"authenticatorData": b64u.EncodeToString(ad), "signature": b64u.EncodeToString(sig)}
	p.expect(p.do("POST", "/api/login/passkey", body), 200, "first use")
	q := e.client()
	q.expect(q.do("POST", "/api/login/passkey", body), 401, "the same response again")

	// A challenge issued for signing in cannot register a passkey, and one issued to another session
	// cannot either.
	signInCh := challengeOf(t, q.do("POST", "/api/login/passkey/options", "{}"))
	k2 := authtest.NewKey()
	cd2, att := k2.Create(signInCh, rp, origin, authtest.UP|authtest.UV)
	add := func(cl *client, cd, att []byte) reply {
		return cl.do("POST", "/api/passkeys", map[string]any{"name": "x", "id": b64u.EncodeToString(k2.ID),
			"clientDataJSON": b64u.EncodeToString(cd), "attestationObject": b64u.EncodeToString(att)})
	}
	c.expect(add(c, cd2, att), 400, "registering with a sign-in challenge")
	other := e.client()
	other.signIn()
	theirs := challengeOf(t, other.do("POST", "/api/passkeys/options", map[string]string{"password": password}))
	cd3, att3 := k2.Create(theirs, rp, origin, authtest.UP|authtest.UV)
	c.expect(add(c, cd3, att3), 400, "registering with another session's challenge")

	// An expired challenge.
	late := e.client()
	ch = challengeOf(t, late.do("POST", "/api/login/passkey/options", "{}"))
	e.now = e.now.Add(passkeyTTL + time.Second)
	cd, ad, sig = k.Get(ch, rp, origin, authtest.UP|authtest.UV)
	late.expect(late.do("POST", "/api/login/passkey", map[string]string{"id": b64u.EncodeToString(k.ID),
		"clientDataJSON": b64u.EncodeToString(cd), "authenticatorData": b64u.EncodeToString(ad), "signature": b64u.EncodeToString(sig)}), 401, "an expired challenge")
}

func TestPasskeyRemovedLockedAndReset(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	k := authtest.NewKey()
	addPasskey(t, c, k)

	// A locked account stays locked for passkeys too.
	w := e.client()
	for range lockAfter {
		w.do("POST", "/api/login", map[string]string{"password": "not the password"})
	}
	p := e.client()
	p.expect(passkeySignIn(t, p, k, authtest.UP|authtest.UV), 423, "a passkey while locked")
	e.now = e.now.Add(lockBase + time.Second)
	p.expect(passkeySignIn(t, p, k, authtest.UP|authtest.UV), 200, "a passkey after the lock")

	c.signIn()
	id := c.do("GET", "/api/passkeys", nil).body["passkeys"].([]any)[0].(map[string]any)["id"].(string)
	c.expect(c.do("DELETE", "/api/passkeys/"+id, "{}"), 204, "remove the passkey")
	p.expect(passkeySignIn(t, p, k, authtest.UP|authtest.UV), 401, "a removed passkey")

	addPasskey(t, c, k)
	if err := e.s.ResetMFA(); err != nil {
		t.Fatal(err)
	}
	p.expect(passkeySignIn(t, p, k, authtest.UP|authtest.UV), 401, "a passkey after reset-2fa")
}

func TestPasskeyChallengesAreCapped(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	for i := range 60 {
		c.ip = "198.51.100." + string(rune('1'+i%9)) // spread over IPs to dodge the per-IP limit
		c.do("POST", "/api/login/passkey/options", "{}")
	}
	e.s.mu.Lock()
	n := len(e.s.st.PasskeyChallenges)
	e.s.mu.Unlock()
	if n > 51 {
		t.Errorf("%d pending challenges kept", n)
	}
}
