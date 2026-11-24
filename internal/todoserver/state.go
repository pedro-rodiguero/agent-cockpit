package todoserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// state is auth.json: the one account, its sessions, devices and pending sign-in challenges. Secrets
// are stored only as hashes, except the 2FA secret, which is sealed with TODO_ENC_KEY.
type state struct {
	Account    *account    `json:"account,omitempty"`
	Sessions   []session   `json:"sessions"`
	Devices    []device    `json:"devices"`
	Challenges []challenge `json:"challenges"`
	Passkeys   []passkey   `json:"passkeys"`
	// PasskeyChallenges are issued for creating or using a passkey: single use, five minutes.
	PasskeyChallenges []passkeyChallenge `json:"passkeyChallenges"`
}

type passkey struct {
	ID        string    `json:"id"` // the credential ID, base64url
	Name      string    `json:"name"`
	PublicKey []byte    `json:"publicKey"` // PKIX DER
	Alg       int       `json:"alg"`
	SignCount uint32    `json:"signCount"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"lastUsed,omitzero"`
}

type passkeyChallenge struct {
	Challenge string    `json:"challenge"` // base64url, as clientDataJSON carries it
	Create    bool      `json:"create"`    // for creating a passkey (else for signing in)
	Session   string    `json:"session,omitempty"`
	Expires   time.Time `json:"expires"`
}

type account struct {
	Hash        string    `json:"hash"`
	ChangedAt   time.Time `json:"changedAt"`
	FailedCount int       `json:"failedCount,omitempty"`
	LockLevel   int       `json:"lockLevel,omitempty"`
	LockedUntil time.Time `json:"lockedUntil,omitzero"`
	MFA         *mfa      `json:"mfa,omitempty"`
	// UserHandle identifies the account to passkeys (WebAuthn user.id): random, made once.
	UserHandle string `json:"userHandle,omitempty"`
}

type mfa struct {
	Active    bool      `json:"active"`
	Secret    string    `json:"secret"` // sealed
	LastStep  int64     `json:"lastStep"`
	Recovery  []string  `json:"recovery,omitempty"` // hashes of the unused recovery codes
	EnabledAt time.Time `json:"enabledAt,omitzero"`
}

type session struct {
	Hash      string    `json:"hash"` // of the cookie's token
	ID        string    `json:"id"`   // public: shown in the session list
	Started   time.Time `json:"started"`
	LastUsed  time.Time `json:"lastUsed"`
	IP        string    `json:"ip"`
	Agent     string    `json:"agent"`
	Ended     time.Time `json:"ended,omitzero"`
	EndReason string    `json:"endReason,omitempty"`
}

type device struct {
	Hash     string    `json:"hash"` // of the token
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

// challenge is the step between a right password and the 2FA code.
type challenge struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}

func loadState(path string) (state, error) {
	var st state
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func saveState(path string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// event is one line of audit.jsonl, which is only ever appended to. It never holds a password, a
// token or a code.
type event struct {
	At      time.Time         `json:"at"`
	Event   string            `json:"event"`
	IP      string            `json:"ip"`
	Agent   string            `json:"agent"`
	Details map[string]string `json:"details,omitempty"`
}

func appendEvent(path string, e event) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// lastEvents returns up to n events, newest first.
func lastEvents(path string, n int) ([]event, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	out := make([]event, 0, min(n, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out, sc.Err()
}

// dataPaths are the server's files inside its data folder.
type dataPaths struct{ dir string }

func (d dataPaths) state() string { return filepath.Join(d.dir, "auth.json") }
func (d dataPaths) audit() string { return filepath.Join(d.dir, "audit.jsonl") }
func (d dataPaths) todo() string  { return filepath.Join(d.dir, "todo.json") }
