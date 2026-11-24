package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// StepSeconds is the TOTP time step (RFC 6238), the one authenticator apps use by default.
const StepSeconds = 30

// B32 is the base32 authenticator apps expect: no padding.
var B32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret is a 20-byte TOTP secret.
func NewTOTPSecret() []byte {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(errors.New("crypto/rand failed: " + err.Error()))
	}
	return b
}

// HOTP is RFC 4226 with HMAC-SHA1 and 6 digits.
func HOTP(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	o := sum[len(sum)-1] & 15
	n := binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// StepAt is the TOTP step of t.
func StepAt(t time.Time) int64 { return t.Unix() / StepSeconds }

// VerifyTOTP accepts the code of the current step or one either side (a phone clock a little off) and
// returns the step it matched. A step at or before lastStep is refused: the same code never works twice.
func VerifyTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return 0, false
	}
	cur := StepAt(now)
	var hit int64
	found := false
	for s := cur - 1; s <= cur+1; s++ {
		if s > lastStep && subtle.ConstantTimeCompare([]byte(HOTP(key, uint64(s))), []byte(code)) == 1 {
			hit, found = s, true
		}
	}
	return hit, found
}

// OTPAuthURI is the otpauth:// link authenticator apps (and iOS Passwords) open to add the account.
func OTPAuthURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{"secret": {B32.EncodeToString(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"},
		"digits": {"6"}, "period": {fmt.Sprint(StepSeconds)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ParseKey reads a 32-byte key given as 64 hex characters or base64. An empty value is no key.
func ParseKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if k, err := hex.DecodeString(v); err == nil && len(k) == 32 {
		return k, nil
	}
	if k, err := base64.StdEncoding.DecodeString(v); err == nil && len(k) == 32 {
		return k, nil
	}
	return nil, errors.New("the key must be 32 bytes, as 64 hex characters or base64; make one with: openssl rand -hex 32")
}

// Seal encrypts plain with AES-256-GCM. aad binds the result to its owner: a secret copied to another
// record does not open.
func Seal(key, plain []byte, aad string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1." + b64.EncodeToString(gcm.Seal(nonce, nonce, plain, []byte(aad))), nil
}

// Open reverses Seal.
func Open(key []byte, sealed, aad string) ([]byte, error) {
	rest, ok := strings.CutPrefix(sealed, "v1.")
	if !ok {
		return nil, errors.New("unknown sealed format")
	}
	raw, err := b64.DecodeString(rest)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("sealed value truncated")
	}
	return gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(aad))
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block) // 16-byte tags only
}

// Hash is the SHA-256 of a token, hex: what is stored in place of session, device and recovery tokens.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MAC is HMAC-SHA256(key, msg), base64url: the CSRF token derived from a session token.
func MAC(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(msg))
	return b64.EncodeToString(m.Sum(nil))
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
