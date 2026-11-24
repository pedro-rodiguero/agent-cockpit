package auth

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// WebAuthn (passkeys), the subset a single-site server needs: "none" attestation, ES256 and RS256 keys,
// user verification required. https://www.w3.org/TR/webauthn-3/

// COSE algorithms offered when creating a passkey, in order of preference.
const (
	AlgES256 = -7
	AlgRS256 = -257
)

// Authenticator data flags.
const (
	flagUP = 0x01 // user present
	flagUV = 0x04 // user verified (Face ID, Touch ID, PIN)
	flagAT = 0x40 // attested credential data included
)

// Credential is a registered passkey's public part.
type Credential struct {
	ID        []byte // the credential ID
	PublicKey []byte // PKIX DER
	Alg       int
	SignCount uint32
}

var errWebAuthn = errors.New("the passkey response does not verify")

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// checkClientData verifies clientDataJSON: the ceremony, the challenge (base64url, compared by the
// caller's lookup), and the origin.
func checkClientData(raw []byte, typ, origin string) (challenge string, err error) {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return "", errWebAuthn
	}
	if cd.Type != typ || cd.Origin != origin || cd.CrossOrigin || cd.Challenge == "" {
		return "", errWebAuthn
	}
	return cd.Challenge, nil
}

// authData is the parsed authenticator data.
type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	credID    []byte
	coseKey   []byte
}

func parseAuthData(b []byte, wantCred bool) (authData, error) {
	if len(b) < 37 {
		return authData{}, errWebAuthn
	}
	ad := authData{rpIDHash: b[:32], flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	rest := b[37:]
	if ad.flags&flagAT != 0 {
		if len(rest) < 18 {
			return authData{}, errWebAuthn
		}
		n := int(binary.BigEndian.Uint16(rest[16:18]))
		if n == 0 || n > 1023 || len(rest) < 18+n {
			return authData{}, errWebAuthn
		}
		ad.credID = rest[18 : 18+n]
		_, used, err := cborItem(rest[18+n:], 0)
		if err != nil {
			return authData{}, errWebAuthn
		}
		ad.coseKey = rest[18+n : 18+n+used]
	} else if wantCred {
		return authData{}, errWebAuthn
	}
	return ad, nil
}

func checkFlags(ad authData, rpID string) error {
	h := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(ad.rpIDHash, h[:]) || ad.flags&flagUP == 0 || ad.flags&flagUV == 0 {
		return errWebAuthn
	}
	return nil
}

// VerifyRegistration checks a navigator.credentials.create response and returns the new credential and
// the challenge it answered, which the caller must match against one it issued.
func VerifyRegistration(clientDataJSON, attestationObject []byte, rpID, origin string) (Credential, string, error) {
	challenge, err := checkClientData(clientDataJSON, "webauthn.create", origin)
	if err != nil {
		return Credential{}, "", err
	}
	obj, _, err := cborItem(attestationObject, 0)
	if err != nil {
		return Credential{}, "", errWebAuthn
	}
	m, ok := obj.(map[any]any)
	if !ok {
		return Credential{}, "", errWebAuthn
	}
	fmtName, _ := m["fmt"].(string)
	raw, _ := m["authData"].([]byte)
	// Passkeys from iCloud Keychain, Google Password Manager and browsers answer "none" when asked for no
	// attestation; a "packed" self-attestation is accepted the same way, without trusting its statement.
	if fmtName != "none" && fmtName != "packed" {
		return Credential{}, "", fmt.Errorf("attestation format %q not supported", fmtName)
	}
	ad, err := parseAuthData(raw, true)
	if err != nil {
		return Credential{}, "", err
	}
	if err := checkFlags(ad, rpID); err != nil {
		return Credential{}, "", err
	}
	pub, alg, err := coseToPKIX(ad.coseKey)
	if err != nil {
		return Credential{}, "", err
	}
	return Credential{ID: append([]byte(nil), ad.credID...), PublicKey: pub, Alg: alg, SignCount: ad.signCount}, challenge, nil
}

// VerifyAssertion checks a navigator.credentials.get response against a registered credential. It
// returns the challenge answered and the authenticator's new sign count. A count that does not grow
// (when either is non-zero) means a cloned authenticator, and fails.
func VerifyAssertion(cred Credential, clientDataJSON, authenticatorData, signature []byte, rpID, origin string) (string, uint32, error) {
	challenge, err := checkClientData(clientDataJSON, "webauthn.get", origin)
	if err != nil {
		return "", 0, err
	}
	ad, err := parseAuthData(authenticatorData, false)
	if err != nil {
		return "", 0, err
	}
	if err := checkFlags(ad, rpID); err != nil {
		return "", 0, err
	}
	pub, err := x509.ParsePKIXPublicKey(cred.PublicKey)
	if err != nil {
		return "", 0, errWebAuthn
	}
	h := sha256.Sum256(clientDataJSON)
	signed := sha256.Sum256(append(append([]byte(nil), authenticatorData...), h[:]...))
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if cred.Alg != AlgES256 || !ecdsa.VerifyASN1(k, signed[:], signature) {
			return "", 0, errWebAuthn
		}
	case *rsa.PublicKey:
		if cred.Alg != AlgRS256 || rsa.VerifyPKCS1v15(k, crypto.SHA256, signed[:], signature) != nil {
			return "", 0, errWebAuthn
		}
	default:
		return "", 0, errWebAuthn
	}
	if (ad.signCount != 0 || cred.SignCount != 0) && ad.signCount <= cred.SignCount {
		return "", 0, errors.New("the passkey's counter went back: it may have been copied")
	}
	return challenge, ad.signCount, nil
}

// coseToPKIX turns a COSE public key (RFC 9053) into PKIX DER.
func coseToPKIX(raw []byte) ([]byte, int, error) {
	v, _, err := cborItem(raw, 0)
	if err != nil {
		return nil, 0, errWebAuthn
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, 0, errWebAuthn
	}
	kty, _ := m[int64(1)].(int64)
	alg, _ := m[int64(3)].(int64)
	switch {
	case kty == 2 && alg == AlgES256:
		crv, _ := m[int64(-1)].(int64)
		x, _ := m[int64(-2)].([]byte)
		y, _ := m[int64(-3)].([]byte)
		if crv != 1 || len(x) != 32 || len(y) != 32 {
			return nil, 0, errWebAuthn
		}
		k := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if _, err := k.ECDH(); err != nil { // not on the curve
			return nil, 0, errWebAuthn
		}
		der, err := x509.MarshalPKIXPublicKey(k)
		return der, AlgES256, err
	case kty == 3 && alg == AlgRS256:
		n, _ := m[int64(-1)].([]byte)
		e, _ := m[int64(-2)].([]byte)
		if len(n) < 256 || len(n) > 512 || len(e) == 0 || len(e) > 4 {
			return nil, 0, errWebAuthn
		}
		k := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		der, err := x509.MarshalPKIXPublicKey(k)
		return der, AlgRS256, err
	}
	return nil, 0, fmt.Errorf("key type %d with algorithm %d not supported", kty, alg)
}

// cborItem decodes one CBOR item (RFC 8949) of the kinds WebAuthn uses: integers, byte and text strings,
// arrays, maps, booleans and null. It returns the item and how many bytes it took. Definite lengths only,
// at most 16 levels deep, nothing longer than the input.
func cborItem(b []byte, depth int) (any, int, error) {
	bad := errors.New("bad CBOR")
	if depth > 16 || len(b) == 0 {
		return nil, 0, bad
	}
	major, info := b[0]>>5, b[0]&0x1f
	n, used := uint64(info), 1
	switch {
	case info < 24:
	case info >= 24 && info <= 27:
		size := 1 << (info - 24)
		if len(b) < 1+size {
			return nil, 0, bad
		}
		n = 0
		for _, c := range b[1 : 1+size] {
			n = n<<8 | uint64(c)
		}
		used += size
	default:
		return nil, 0, bad // indefinite lengths and reserved values
	}
	rest := b[used:]
	switch major {
	case 0:
		if n > 1<<62 {
			return nil, 0, bad
		}
		return int64(n), used, nil
	case 1:
		if n > 1<<62 {
			return nil, 0, bad
		}
		return -1 - int64(n), used, nil
	case 2, 3:
		if n > uint64(len(rest)) {
			return nil, 0, bad
		}
		if major == 2 {
			return append([]byte(nil), rest[:n]...), used + int(n), nil
		}
		return string(rest[:n]), used + int(n), nil
	case 4:
		if n > uint64(len(rest)) {
			return nil, 0, bad
		}
		out := make([]any, 0, n)
		for range n {
			v, k, err := cborItem(b[used:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			out = append(out, v)
			used += k
		}
		return out, used, nil
	case 5:
		if n > uint64(len(rest)) {
			return nil, 0, bad
		}
		out := make(map[any]any, n)
		for range n {
			k, ku, err := cborItem(b[used:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			used += ku
			v, vu, err := cborItem(b[used:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			used += vu
			switch k.(type) {
			case int64, string:
				out[k] = v
			default:
				return nil, 0, bad
			}
		}
		return out, used, nil
	case 7:
		switch info {
		case 20:
			return false, used, nil
		case 21:
			return true, used, nil
		case 22:
			return nil, used, nil
		}
	}
	return nil, 0, bad
}
