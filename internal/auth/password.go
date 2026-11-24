// Package auth holds the sign-in building blocks of the to-do server: password hashing and policy,
// TOTP, sealing secrets, rate limits and the client IP. They follow OWASP ASVS level 2; the server
// (internal/todoserver) puts them together.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
)

// Password length, in characters after NFKC normalization (NIST 800-63B).
const (
	MinPassword = 12
	MaxPassword = 128
)

const (
	scryptR, scryptP = 8, 1
	keyLen           = 32
)

// scryptLogN is the scrypt cost: N = 2^scryptLogN. Tests lower it; nothing else does.
var scryptLogN = 17

// SetCostForTests lowers the scrypt cost, which at N=2^17 takes a few hundred milliseconds per hash.
// Only tests call it.
func SetCostForTests(logN int) { scryptLogN = logN }

var b64 = base64.RawURLEncoding

// HashPassword hashes a password as scrypt$N=…,r=…,p=…$salt$hash, so the parameters travel with it.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	n := 1 << scryptLogN
	key, err := scrypt.Key([]byte(norm.NFKC.String(password)), salt, n, scryptR, scryptP, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$N=%d,r=%d,p=%d$%s$%s", n, scryptR, scryptP, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type parsedHash struct {
	n, r, p   int
	salt, key []byte
}

// parseHash reads a stored hash. Its parameters come from a file, so they are bounded: a tampered record
// must not turn a login into a denial of service.
func parseHash(h string) (parsedHash, bool) {
	parts := strings.Split(h, "$")
	if len(parts) != 4 || parts[0] != "scrypt" {
		return parsedHash{}, false
	}
	var ph parsedHash
	for kv := range strings.SplitSeq(parts[1], ",") {
		k, v, _ := strings.Cut(kv, "=")
		x, err := strconv.Atoi(v)
		if err != nil {
			return parsedHash{}, false
		}
		switch k {
		case "N":
			ph.n = x
		case "r":
			ph.r = x
		case "p":
			ph.p = x
		}
	}
	if ph.n < 2 || ph.n > 1<<18 || ph.n&(ph.n-1) != 0 || ph.r < 1 || ph.r > 8 || ph.p < 1 || ph.p > 4 {
		return parsedHash{}, false
	}
	var err1, err2 error
	ph.salt, err1 = b64.DecodeString(parts[2])
	ph.key, err2 = b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(ph.salt) < 8 || len(ph.key) < 16 {
		return parsedHash{}, false
	}
	return ph, true
}

var dummySalt = make([]byte, 16)

// VerifyPassword checks password against stored. It does the same work when stored is empty or broken
// (a hash with the current parameters), so timing does not tell whether an account exists. rehash
// reports a correct password stored with old parameters.
func VerifyPassword(stored, password string) (ok, rehash bool) {
	pw := []byte(norm.NFKC.String(password))
	ph, valid := parseHash(stored)
	if !valid {
		_, _ = scrypt.Key(pw, dummySalt, 1<<scryptLogN, scryptR, scryptP, keyLen)
		return false, false
	}
	key, err := scrypt.Key(pw, ph.salt, ph.n, ph.r, ph.p, len(ph.key))
	if err != nil {
		return false, false
	}
	ok = subtle.ConstantTimeCompare(key, ph.key) == 1 && len(password) > 0
	return ok, ok && (ph.n != 1<<scryptLogN || ph.r != scryptR || ph.p != scryptP || len(ph.key) != keyLen)
}

// commonPasswords are the 10,000 most frequent passwords of 12 characters or more, lowercased and NFKC,
// in order of frequency, from SecLists' Passwords/Common-Credentials/10-million-password-list-top-1000000.txt
// (MIT license, github.com/danielmiessler/SecLists), plus a few Brazilian patterns. Shorter ones are
// already refused by length.
//
//go:embed common-passwords.txt
var commonPasswordsText string

var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for p := range strings.SplitSeq(commonPasswordsText, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	for _, p := range []string{"senhasenha12", "senha1234567", "senha12345678", "minhasenha123", "trocarsenha123",
		"brasil123456", "flamengo1234", "corinthians123", "palmeiras123", "qwerty123456", "admin1234567",
		"administrador", "administrador123", "bemvindo1234", "bemvindo@123"} {
		m[p] = true
	}
	return m
}()

// ErrWeakPassword is the reason a new password is refused; its text is shown to the person.
type ErrWeakPassword struct{ Reason string }

func (e ErrWeakPassword) Error() string { return e.Reason }

// CheckPassword is the one check every new password goes through: length after NFKC, not a common
// password, not made of fewer than four distinct characters, and not containing any of words (the
// app's or the person's name). No composition rules (NIST 800-63B).
func CheckPassword(password string, words ...string) error {
	pw := norm.NFKC.String(password)
	size := utf8.RuneCountInString(pw)
	switch {
	case !utf8.ValidString(password) || size < MinPassword:
		return ErrWeakPassword{fmt.Sprintf("The password needs at least %d characters.", MinPassword)}
	case size > MaxPassword:
		return ErrWeakPassword{fmt.Sprintf("The password can have at most %d characters.", MaxPassword)}
	}
	distinct := map[rune]bool{}
	for _, r := range pw {
		distinct[r] = true
	}
	if commonPasswords[strings.ToLower(pw)] || len(distinct) < 4 {
		return ErrWeakPassword{"That password is among the most used and easy to guess. Pick another."}
	}
	flat := compact(pw)
	for _, w := range words {
		if c := compact(w); len(c) >= 4 && strings.Contains(flat, c) {
			return ErrWeakPassword{"The password cannot contain the name of the app."}
		}
	}
	return nil
}

// compact folds case and accents and keeps letters and digits: "Ágenda-Cockpit" → "agendacockpit".
func compact(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// RandomToken is 32 random bytes, base64url: session and device tokens, recovery and challenge ids.
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(errors.New("crypto/rand failed: " + err.Error()))
	}
	return b64.EncodeToString(b)
}
