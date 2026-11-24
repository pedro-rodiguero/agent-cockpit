package todoserver

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth"
)

const (
	passkeyTTL   = 5 * time.Minute
	maxPasskeys  = 10
	webauthnWait = 120000 // ms the browser waits for the person
)

var b64u = base64.RawURLEncoding

// rpID is the site passkeys belong to: the host of the public origin.
func (s *Server) rpID() string {
	u, _ := url.Parse(s.cfg.Origin)
	return u.Hostname()
}

// issueChallenge records a new single-use challenge. Callers hold s.mu.
func (s *Server) issueChallenge(now time.Time, create bool, session string) string {
	s.st.PasskeyChallenges = slices.DeleteFunc(s.st.PasskeyChallenges, func(c passkeyChallenge) bool { return now.After(c.Expires) })
	if len(s.st.PasskeyChallenges) > 50 { // a flood of unused sign-in prompts: keep the newest
		s.st.PasskeyChallenges = s.st.PasskeyChallenges[len(s.st.PasskeyChallenges)-50:]
	}
	ch := auth.RandomToken()
	s.st.PasskeyChallenges = append(s.st.PasskeyChallenges, passkeyChallenge{Challenge: ch, Create: create, Session: session, Expires: now.Add(passkeyTTL)})
	return ch
}

// takeChallenge uses up a challenge the client answered, if it was issued for this ceremony (and, for
// creating a passkey, this session) and has not expired. Callers hold s.mu.
func (s *Server) takeChallenge(answered string, now time.Time, create bool, session string) bool {
	i := slices.IndexFunc(s.st.PasskeyChallenges, func(c passkeyChallenge) bool { return auth.Equal(c.Challenge, answered) })
	if i < 0 {
		return false
	}
	c := s.st.PasskeyChallenges[i]
	s.st.PasskeyChallenges = slices.Delete(s.st.PasskeyChallenges, i, i+1)
	return c.Create == create && c.Session == session && !now.After(c.Expires)
}

func (s *Server) postLoginPasskeyOptions(c *call) (any, error) {
	if !s.login.Take("login:"+c.ip, loginPerIP, loginWindow) {
		return nil, fail(http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
	}
	if err := decode(c, &struct{}{}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.issueChallenge(c.now, false, "")
	if err := s.save(); err != nil {
		return nil, err
	}
	return map[string]any{"publicKey": map[string]any{
		"challenge": ch, "rpId": s.rpID(), "timeout": webauthnWait, "userVerification": "required",
		"allowCredentials": []any{},
	}}, nil
}

func (s *Server) postLoginPasskey(c *call) (any, error) {
	var in struct {
		ID                string `json:"id"`
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	}
	if !s.login.Take("login:"+c.ip, loginPerIP, loginWindow) {
		return nil, fail(http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	cd, err1 := b64u.DecodeString(in.ClientDataJSON)
	ad, err2 := b64u.DecodeString(in.AuthenticatorData)
	sig, err3 := b64u.DecodeString(in.Signature)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, fail(http.StatusBadRequest, "That request is not valid.")
	}
	refused := fail(http.StatusUnauthorized, "That passkey did not sign in. Try again or use the password.")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Account == nil {
		return nil, refused
	}
	i := slices.IndexFunc(s.st.Passkeys, func(p passkey) bool { return p.ID == in.ID })
	if i < 0 || (in.UserHandle != "" && in.UserHandle != s.st.Account.UserHandle) {
		s.audit(c, "login_fail", map[string]string{"reason": "unknown passkey"})
		return nil, refused
	}
	pk := &s.st.Passkeys[i]
	credID, _ := b64u.DecodeString(pk.ID)
	answered, count, err := auth.VerifyAssertion(auth.Credential{ID: credID, PublicKey: pk.PublicKey, Alg: pk.Alg, SignCount: pk.SignCount},
		cd, ad, sig, s.rpID(), s.cfg.Origin)
	if err == nil && !s.takeChallenge(answered, c.now, false, "") {
		err = errSignedOut
	}
	if err != nil {
		_ = s.save() // the challenge, if any, is used up
		s.audit(c, "login_fail", map[string]string{"reason": "passkey did not verify", "passkey": pk.Name})
		s.failAttempt(c, "passkey")
		return nil, refused
	}
	if until, locked := s.lockedUntil(c.now); locked {
		_ = s.save()
		return nil, lockedError(until)
	}
	pk.SignCount, pk.LastUsed = count, c.now
	s.clearFailures()
	csrf, err := s.openSession(c)
	if err != nil {
		return nil, err
	}
	s.audit(c, "login_ok", map[string]string{"with": "passkey", "passkey": pk.Name})
	return loginReply{Next: "done", CSRF: csrf}, nil
}

type passkeyInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

func (s *Server) getPasskeys(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []passkeyInfo{}
	for _, p := range s.st.Passkeys {
		out = append(out, passkeyInfo{p.ID, p.Name, p.Created, p.LastUsed})
	}
	return map[string]any{"passkeys": out}, nil
}

// postPasskeyOptions starts adding a passkey. It asks for the password again: a session left open on a
// borrowed phone must not be enough to plant a way back in.
func (s *Server) postPasskeyOptions(c *call) (any, error) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	if err := s.reauth(c, in.Password); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.st.Passkeys) >= maxPasskeys {
		return nil, fail(http.StatusConflict, "Too many passkeys. Remove one first.")
	}
	if s.st.Account.UserHandle == "" {
		s.st.Account.UserHandle = auth.RandomToken()
	}
	exclude := []map[string]any{}
	for _, p := range s.st.Passkeys {
		exclude = append(exclude, map[string]any{"type": "public-key", "id": p.ID})
	}
	ch := s.issueChallenge(c.now, true, c.session.Hash)
	if err := s.save(); err != nil {
		return nil, err
	}
	return map[string]any{"publicKey": map[string]any{
		"challenge": ch,
		"rp":        map[string]string{"id": s.rpID(), "name": appName},
		"user":      map[string]string{"id": s.st.Account.UserHandle, "name": "to-do", "displayName": appName + " to-do"},
		"pubKeyCredParams": []map[string]any{
			{"type": "public-key", "alg": auth.AlgES256}, {"type": "public-key", "alg": auth.AlgRS256},
		},
		"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": "required"},
		"excludeCredentials":     exclude,
		"attestation":            "none",
		"timeout":                webauthnWait,
	}}, nil
}

func (s *Server) postPasskey(c *call) (any, error) {
	var in struct {
		Name              string   `json:"name"`
		ID                string   `json:"id"`
		ClientDataJSON    string   `json:"clientDataJSON"`
		AttestationObject string   `json:"attestationObject"`
		Transports        []string `json:"transports"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > 60 {
		return nil, fail(http.StatusBadRequest, "Give the passkey a name of up to 60 characters.")
	}
	cd, err1 := b64u.DecodeString(in.ClientDataJSON)
	att, err2 := b64u.DecodeString(in.AttestationObject)
	if err1 != nil || err2 != nil || len(att) > 16<<10 {
		return nil, fail(http.StatusBadRequest, "That request is not valid.")
	}
	cred, answered, err := auth.VerifyRegistration(cd, att, s.rpID(), s.cfg.Origin)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && !s.takeChallenge(answered, c.now, true, c.session.Hash) {
		err = errSignedOut
	}
	if err != nil {
		_ = s.save()
		return nil, fail(http.StatusBadRequest, "The passkey could not be added. Start again.")
	}
	id := b64u.EncodeToString(cred.ID)
	if id != in.ID || slices.ContainsFunc(s.st.Passkeys, func(p passkey) bool { return p.ID == id }) {
		return nil, fail(http.StatusBadRequest, "The passkey could not be added. Start again.")
	}
	pk := passkey{ID: id, Name: name, PublicKey: cred.PublicKey, Alg: cred.Alg, SignCount: cred.SignCount, Created: c.now}
	s.st.Passkeys = append(s.st.Passkeys, pk)
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "passkey_added", map[string]string{"passkey": name})
	return map[string]any{"passkey": passkeyInfo{pk.ID, pk.Name, pk.Created, pk.LastUsed}}, nil
}

func (s *Server) deletePasskey(c *call) (any, error) {
	id := c.r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.st.Passkeys, func(p passkey) bool { return p.ID == id })
	if i < 0 {
		return nil, errNotFound
	}
	name := s.st.Passkeys[i].Name
	s.st.Passkeys = slices.Delete(s.st.Passkeys, i, i+1)
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "passkey_removed", map[string]string{"passkey": name})
	return noContent{}, nil
}
