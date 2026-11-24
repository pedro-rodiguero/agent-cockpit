// Package authtest is a software passkey authenticator for tests: it creates and uses a P-256 passkey
// the way a phone does, so WebAuthn verification can be tested end to end without a browser.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
)

// Flags of the authenticator data.
const (
	UP = 0x01 // user present
	UV = 0x04 // user verified
	AT = 0x40 // attested credential data
)

// Key is one passkey held by the software authenticator.
type Key struct {
	Priv  *ecdsa.PrivateKey
	ID    []byte
	Count uint32
}

// NewKey makes a passkey with a random credential ID.
func NewKey() *Key {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Key{Priv: k, ID: id}
}

// ClientData is clientDataJSON for a ceremony.
func ClientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// AuthData is authenticator data for rpID, with the credential's public key when withCred.
func (k *Key) AuthData(rpID string, flags byte, withCred bool) []byte {
	h := sha256.Sum256([]byte(rpID))
	b := append([]byte(nil), h[:]...)
	b = append(b, flags)
	b = binary.BigEndian.AppendUint32(b, k.Count)
	if withCred {
		b = append(b, make([]byte, 16)...)
		b = binary.BigEndian.AppendUint16(b, uint16(len(k.ID)))
		b = append(b, k.ID...)
		x, y := make([]byte, 32), make([]byte, 32)
		k.Priv.X.FillBytes(x)
		k.Priv.Y.FillBytes(y)
		b = append(b, CBOR(map[any]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})...)
	}
	return b
}

// Create answers navigator.credentials.create: clientDataJSON and a "none" attestation object.
func (k *Key) Create(challenge, rpID, origin string, flags byte) (clientData, attestation []byte) {
	clientData = ClientData("webauthn.create", challenge, origin)
	attestation = CBOR(map[any]any{"fmt": "none", "attStmt": map[any]any{}, "authData": k.AuthData(rpID, flags|AT, true)})
	return clientData, attestation
}

// Get answers navigator.credentials.get: clientDataJSON, authenticator data and the signature.
func (k *Key) Get(challenge, rpID, origin string, flags byte) (clientData, authData, sig []byte) {
	k.Count++
	clientData = ClientData("webauthn.get", challenge, origin)
	authData = k.AuthData(rpID, flags, false)
	h := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte(nil), authData...), h[:]...))
	sig, _ = ecdsa.SignASN1(rand.Reader, k.Priv, digest[:])
	return clientData, authData, sig
}

// CBOR encodes ints, byte strings, text and maps, with map keys in canonical order.
func CBOR(v any) []byte {
	switch x := v.(type) {
	case int:
		if x >= 0 {
			return head(0, uint64(x))
		}
		return head(1, uint64(-1-x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case map[any]any:
		keys := make([]any, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return string(CBOR(keys[i])) < string(CBOR(keys[j])) })
		out := head(5, uint64(len(x)))
		for _, k := range keys {
			out = append(append(out, CBOR(k)...), CBOR(x[k])...)
		}
		return out
	}
	panic("authtest.CBOR: unsupported type")
}

func head(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	case n < 65536:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
	b := []byte{major<<5 | 26, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(n))
	return b
}
