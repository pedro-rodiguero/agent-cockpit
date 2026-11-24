package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() { scryptLogN = 10 } // N=2^17 takes a few hundred ms per hash

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "scrypt$N=1024,r=8,p=1$") {
		t.Errorf("format: %s", h)
	}
	if ok, rehash := VerifyPassword(h, "correct horse battery"); !ok || rehash {
		t.Errorf("right password: ok %v rehash %v", ok, rehash)
	}
	if ok, _ := VerifyPassword(h, "correct horse batterY"); ok {
		t.Error("wrong password accepted")
	}
	if ok, _ := VerifyPassword("", ""); ok {
		t.Error("empty stored hash accepted")
	}
	// The same password in another Unicode form is the same password (NFKC).
	h2, _ := HashPassword("ﬁve ﬁngers here")
	if ok, _ := VerifyPassword(h2, "five fingers here"); !ok {
		t.Error("NFKC forms differ")
	}
	scryptLogN = 11
	if ok, rehash := VerifyPassword(h, "correct horse battery"); !ok || !rehash {
		t.Errorf("old cost not flagged for rehash: ok %v rehash %v", ok, rehash)
	}
	scryptLogN = 10
}

func TestHashParametersAreBounded(t *testing.T) {
	for _, h := range []string{
		"scrypt$N=1048576,r=8,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // N too large: a DoS
		"scrypt$N=1000,r=8,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",    // not a power of two
		"scrypt$N=1024,r=99,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"plain$whatever", "scrypt$$$",
	} {
		if _, ok := parseHash(h); ok {
			t.Errorf("accepted %s", h)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for pw, ok := range map[string]bool{
		"short":                        false,
		strings.Repeat("ab1", 43):      false, // 129 characters
		"qwerty123456":                 false, // common
		"QWERTY123456":                 false, // common, any case
		"aaaaaaaaaaaaaaaa":             false, // too few distinct characters
		"my agent cockpit list":        false, // contains the app's name
		"plum river sixteen lanterns":  true,
		"ação rápida no horário certo": true,
	} {
		err := CheckPassword(pw, "agent cockpit")
		if (err == nil) != ok {
			t.Errorf("CheckPassword(%q) = %v", pw, err)
		}
	}
}

func TestTOTP(t *testing.T) {
	// RFC 6238, appendix B, SHA-1 secret; the last six digits of the eight-digit values.
	key := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := HOTP(key, uint64(unix/StepSeconds)); got != want {
			t.Errorf("t=%d: %s, want %s", unix, got, want)
		}
	}
	now := time.Unix(1234567890, 0)
	code := HOTP(key, uint64(StepAt(now)))
	step, ok := VerifyTOTP(key, code, now, 0)
	if !ok || step != StepAt(now) {
		t.Fatal("current code refused")
	}
	if _, ok := VerifyTOTP(key, code, now, step); ok {
		t.Error("the same code worked twice")
	}
	if _, ok := VerifyTOTP(key, HOTP(key, uint64(StepAt(now)+1)), now, 0); !ok {
		t.Error("next step's code refused (clock skew)")
	}
	if _, ok := VerifyTOTP(key, HOTP(key, uint64(StepAt(now)+2)), now, 0); ok {
		t.Error("code two steps away accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "12a456", " 12345"} {
		if _, ok := VerifyTOTP(key, bad, now, 0); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if u := OTPAuthURI("Agent Cockpit", "me", key); !strings.HasPrefix(u, "otpauth://totp/Agent%20Cockpit:me?") || !strings.Contains(u, "secret="+B32.EncodeToString(key)) {
		t.Errorf("uri: %s", u)
	}
}

func TestSeal(t *testing.T) {
	key, err := ParseKey(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Seal(key, []byte("secret"), "account:1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(key, s, "account:1"); err != nil || string(got) != "secret" {
		t.Errorf("open: %q %v", got, err)
	}
	if _, err := Open(key, s, "account:2"); err == nil {
		t.Error("opened with another owner")
	}
	other, _ := ParseKey(strings.Repeat("cd", 32))
	if _, err := Open(other, s, "account:1"); err == nil {
		t.Error("opened with another key")
	}
	if _, err := Open(key, s[:len(s)-4], "account:1"); err == nil {
		t.Error("opened a truncated value")
	}
	for _, bad := range []string{"tooshort", strings.Repeat("ab", 31)} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("accepted key %q", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := Limiter{Now: func() time.Time { return now }}
	for i := range 3 {
		if !l.Take("ip", 3, time.Minute) {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if l.Take("ip", 3, time.Minute) || !l.Take("other", 3, time.Minute) {
		t.Error("limit not per key")
	}
	now = now.Add(time.Minute)
	if !l.Take("ip", 3, time.Minute) {
		t.Error("window did not slide")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := ClientIP(r, ProxyCaddy); ip != "203.0.113.9" {
		t.Errorf("header trusted from a remote peer: %s", ip)
	}
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7") // the client wrote 1.2.3.4; Caddy appended the real one
	if ip := ClientIP(r, ProxyCaddy); ip != "198.51.100.7" {
		t.Errorf("behind caddy: %s", ip)
	}
	if ip := ClientIP(r, ProxyNone); ip != "127.0.0.1" {
		t.Errorf("no proxy: %s", ip)
	}
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if ip := ClientIP(r, ProxyCaddy); ip != "127.0.0.1" {
		t.Errorf("garbage header: %s", ip)
	}
}
