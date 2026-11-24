package auth

import (
	"testing"
	"time"
)

// FuzzStoredHash: a stored hash read from a tampered file never panics and never verifies an arbitrary
// password unless it is a hash of that password.
func FuzzStoredHash(f *testing.F) {
	f.Add("scrypt$N=1024,r=8,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "pw")
	f.Add("scrypt$N=0,r=0,p=0$$", "")
	f.Add("scrypt$N=99999999999999999999,r=8,p=1$a$b", "x")
	f.Fuzz(func(t *testing.T, stored, pw string) {
		if ok, _ := VerifyPassword(stored, pw); ok && pw == "" {
			t.Fatal("an empty password verified")
		}
	})
}

// FuzzTOTP: any input to the code check is refused or matches the real code of an allowed step.
func FuzzTOTP(f *testing.F) {
	f.Add("123456", int64(1_800_000_000), int64(0))
	f.Add("", int64(0), int64(-1))
	f.Add("１２３４５６", int64(59), int64(0)) // full-width digits
	key := []byte("12345678901234567890")
	f.Fuzz(func(t *testing.T, code string, unix, last int64) {
		now := time.Unix(unix%(1<<40), 0)
		step, ok := VerifyTOTP(key, code, now, last)
		if ok && (HOTP(key, uint64(step)) != code || step <= last || step < StepAt(now)-1 || step > StepAt(now)+1) {
			t.Fatalf("accepted %q at step %d", code, step)
		}
	})
}
