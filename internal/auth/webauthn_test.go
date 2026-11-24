package auth

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth/authtest"
)

const (
	rp     = "todo.test"
	origin = "https://todo.test"
	chall  = "Y2hhbGxlbmdlLWNoYWxsZW5nZS0xMjM0"
)

func TestPasskeyRegistrationAndSignIn(t *testing.T) {
	k := authtest.NewKey()
	cd, att := k.Create(chall, rp, origin, authtest.UP|authtest.UV)
	cred, got, err := VerifyRegistration(cd, att, rp, origin)
	if err != nil || got != chall || !bytes.Equal(cred.ID, k.ID) || cred.Alg != AlgES256 {
		t.Fatalf("registration: %+v %q %v", cred, got, err)
	}

	cd, ad, sig := k.Get("bmV4dC1jaGFsbGVuZ2U", rp, origin, authtest.UP|authtest.UV)
	got, count, err := VerifyAssertion(cred, cd, ad, sig, rp, origin)
	if err != nil || got != "bmV4dC1jaGFsbGVuZ2U" || count != 1 {
		t.Fatalf("assertion: %q %d %v", got, count, err)
	}
	cred.SignCount = count

	// A replayed or copied response: the counter does not grow.
	if _, _, err := VerifyAssertion(cred, cd, ad, sig, rp, origin); err == nil {
		t.Error("the same assertion verified twice (counter did not grow)")
	}
	// Tampered signature, other origin, other site, no user verification, wrong ceremony.
	cd, ad, sig = k.Get(chall, rp, origin, authtest.UP|authtest.UV)
	bad := append([]byte(nil), sig...)
	bad[len(bad)-1] ^= 1
	for name, try := range map[string]func() error{
		"tampered signature": func() error { _, _, err := VerifyAssertion(cred, cd, ad, bad, rp, origin); return err },
		"other origin":       func() error { _, _, err := VerifyAssertion(cred, cd, ad, sig, rp, "https://evil.test"); return err },
		"other rp":           func() error { _, _, err := VerifyAssertion(cred, cd, ad, sig, "evil.test", origin); return err },
		"wrong ceremony": func() error {
			c2, a2, s2 := k.Get(chall, rp, origin, authtest.UP|authtest.UV)
			var m map[string]any
			_ = json.Unmarshal(c2, &m)
			m["type"] = "webauthn.create"
			c3, _ := json.Marshal(m)
			_, _, err := VerifyAssertion(cred, c3, a2, s2, rp, origin)
			return err
		},
		"no user verification": func() error {
			c2, a2, s2 := k.Get(chall, rp, origin, authtest.UP)
			_, _, err := VerifyAssertion(cred, c2, a2, s2, rp, origin)
			return err
		},
		"another key": func() error {
			other := authtest.NewKey()
			other.Count = 100
			c2, a2, s2 := other.Get(chall, rp, origin, authtest.UP|authtest.UV)
			_, _, err := VerifyAssertion(cred, c2, a2, s2, rp, origin)
			return err
		},
	} {
		if try() == nil {
			t.Errorf("%s verified", name)
		}
	}
}

func TestPasskeyRegistrationRefusals(t *testing.T) {
	k := authtest.NewKey()
	for name, try := range map[string]func() error{
		"no user verification": func() error {
			cd, att := k.Create(chall, rp, origin, authtest.UP)
			_, _, err := VerifyRegistration(cd, att, rp, origin)
			return err
		},
		"other origin": func() error {
			cd, att := k.Create(chall, rp, "https://evil.test", authtest.UP|authtest.UV)
			_, _, err := VerifyRegistration(cd, att, rp, origin)
			return err
		},
		"other rp": func() error {
			cd, att := k.Create(chall, "evil.test", origin, authtest.UP|authtest.UV)
			_, _, err := VerifyRegistration(cd, att, rp, origin)
			return err
		},
		"get instead of create": func() error {
			_, att := k.Create(chall, rp, origin, authtest.UP|authtest.UV)
			_, _, err := VerifyRegistration(authtest.ClientData("webauthn.get", chall, origin), att, rp, origin)
			return err
		},
		"unknown attestation format": func() error {
			att := authtest.CBOR(map[any]any{"fmt": "tpm", "attStmt": map[any]any{}, "authData": k.AuthData(rp, authtest.UP|authtest.UV|authtest.AT, true)})
			_, _, err := VerifyRegistration(authtest.ClientData("webauthn.create", chall, origin), att, rp, origin)
			return err
		},
		"garbage": func() error {
			_, _, err := VerifyRegistration([]byte("{}"), []byte{0xff, 0x00}, rp, origin)
			return err
		},
	} {
		if try() == nil {
			t.Errorf("%s registered", name)
		}
	}
}

// FuzzCBOR: any bytes decode or fail, never panic or loop.
func FuzzCBOR(f *testing.F) {
	k := authtest.NewKey()
	f.Add(authtest.CBOR(map[any]any{"fmt": "none", "authData": k.AuthData(rp, 5|authtest.AT, true)}))
	f.Add([]byte{0x9f, 0xff})                                           // indefinite array
	f.Add([]byte{0x5b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // huge byte string
	f.Add(bytes.Repeat([]byte{0x81}, 40))                               // deep nesting
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _, _ = cborItem(b, 0)
		_, _, _ = VerifyRegistration(authtest.ClientData("webauthn.create", chall, origin), b, rp, origin)
	})
}
