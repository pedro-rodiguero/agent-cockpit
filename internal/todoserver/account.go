package todoserver

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth"
)

// passwordWords may not appear in a password.
var passwordWords = []string{"agent cockpit", "cockpit"}

// save writes the state. Callers hold s.mu.
func (s *Server) save() error { return saveState(s.paths.state(), s.st) }

// resolve finds the live session for a cookie token, ending it when it expired.
func (s *Server) resolve(token string, now time.Time) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := auth.Hash(token)
	for i := range s.st.Sessions {
		ss := &s.st.Sessions[i]
		if ss.Hash != h || !ss.Ended.IsZero() {
			continue
		}
		if now.Sub(ss.LastUsed) > sessionIdle || now.Sub(ss.Started) > sessionMax {
			ss.Ended, ss.EndReason = now, "expired"
			_ = s.save()
			return nil
		}
		if now.Sub(ss.LastUsed) >= touchEvery {
			ss.LastUsed = now
			_ = s.save()
		}
		cp := *ss
		return &cp
	}
	return nil
}

func (s *Server) deviceFor(token string, now time.Time) *device {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := auth.Hash(token)
	for i := range s.st.Devices {
		d := &s.st.Devices[i]
		if auth.Equal(d.Hash, h) {
			if now.Sub(d.LastUsed) >= touchEvery {
				d.LastUsed = now
				_ = s.save()
			}
			cp := *d
			return &cp
		}
	}
	return nil
}

// openSession starts a session for c and sets its cookie. Callers hold s.mu.
func (s *Server) openSession(c *call) (string, error) {
	token := auth.RandomToken()
	s.st.Sessions = append(s.st.Sessions, session{
		Hash: auth.Hash(token), ID: auth.RandomToken()[:16], Started: c.now, LastUsed: c.now,
		IP: c.ip, Agent: truncate(c.r.UserAgent(), 200),
	})
	s.pruneSessions(c.now)
	if err := s.save(); err != nil {
		return "", err
	}
	http.SetCookie(c.w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionMax / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	c.token = token
	return csrfFor(token), nil
}

// pruneSessions drops sessions ended long ago and challenges that expired. Callers hold s.mu.
func (s *Server) pruneSessions(now time.Time) {
	s.st.Sessions = slices.DeleteFunc(s.st.Sessions, func(ss session) bool {
		return !ss.Ended.IsZero() && now.Sub(ss.Ended) > endedKeep
	})
	s.st.Challenges = slices.DeleteFunc(s.st.Challenges, func(ch challenge) bool { return now.After(ch.Expires) })
}

// endSessions ends every live session but except (a session hash, or ""), and reports how many. Callers
// hold s.mu.
func (s *Server) endSessions(now time.Time, except, reason string) int {
	n := 0
	for i := range s.st.Sessions {
		ss := &s.st.Sessions[i]
		if ss.Ended.IsZero() && ss.Hash != except {
			ss.Ended, ss.EndReason = now, reason
			n++
		}
	}
	return n
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// ---- locking ----

func (s *Server) lockedUntil(now time.Time) (time.Time, bool) {
	a := s.st.Account
	if a == nil {
		return time.Time{}, false
	}
	return a.LockedUntil, now.Before(a.LockedUntil)
}

func lockedError(until time.Time) error {
	return fail(http.StatusLocked, "Too many wrong attempts: signing in is locked until "+until.UTC().Format("15:04 MST, Jan 2")+".")
}

// failAttempt counts a wrong password or code and locks the account after lockAfter of them, for longer
// each time. It reports whether this attempt locked it. Callers hold s.mu.
func (s *Server) failAttempt(c *call, where string) bool {
	a := s.st.Account
	a.FailedCount++
	if a.FailedCount < lockAfter {
		_ = s.save()
		return false
	}
	a.LockLevel++
	d := min(lockBase<<(a.LockLevel-1), lockMax)
	a.FailedCount, a.LockedUntil = 0, c.now.Add(d)
	_ = s.save()
	s.audit(c, "locked", map[string]string{"until": a.LockedUntil.UTC().Format(time.RFC3339), "level": fmt.Sprint(a.LockLevel), "where": where})
	return true
}

func (s *Server) clearFailures() {
	a := s.st.Account
	a.FailedCount, a.LockLevel, a.LockedUntil = 0, 0, time.Time{}
}

// ---- signing in ----

type loginReply struct {
	Next string `json:"next"`
	CSRF string `json:"csrf,omitempty"`
}

func (s *Server) postLogin(c *call) (any, error) {
	var in struct {
		Password string `json:"password"`
	}
	if !s.login.Take("login:"+c.ip, loginPerIP, loginWindow) {
		return nil, fail(http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := ""
	if s.st.Account != nil {
		stored = s.st.Account.Hash
	}
	ok, rehash := auth.VerifyPassword(stored, in.Password) // always pays the hash, account or not
	if s.st.Account == nil {
		s.audit(c, "login_fail", map[string]string{"reason": "no account"})
		return nil, fail(http.StatusUnauthorized, "Wrong password.")
	}
	if until, locked := s.lockedUntil(c.now); locked {
		s.audit(c, "login_fail", map[string]string{"reason": "locked"})
		if ok {
			return nil, lockedError(until) // only the right password learns the account is locked
		}
		return nil, fail(http.StatusUnauthorized, "Wrong password.")
	}
	if !ok {
		s.audit(c, "login_fail", map[string]string{"reason": "wrong password"})
		s.failAttempt(c, "login")
		return nil, fail(http.StatusUnauthorized, "Wrong password.")
	}
	if rehash {
		if h, err := auth.HashPassword(in.Password); err == nil {
			s.st.Account.Hash = h
		}
	}
	if m := s.st.Account.MFA; m != nil && m.Active {
		token := auth.RandomToken()
		s.st.Challenges = append(s.st.Challenges, challenge{Hash: auth.Hash(token), Expires: c.now.Add(challengeTTL)})
		if err := s.save(); err != nil {
			return nil, err
		}
		http.SetCookie(c.w, &http.Cookie{Name: challengeCookie, Value: token, Path: "/", MaxAge: int(challengeTTL / time.Second),
			Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		return loginReply{Next: "mfa"}, nil
	}
	s.clearFailures()
	csrf, err := s.openSession(c)
	if err != nil {
		return nil, err
	}
	s.audit(c, "login_ok", map[string]string{"with": "password"})
	return loginReply{Next: "done", CSRF: csrf}, nil
}

func (s *Server) postLoginMFA(c *call) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if !s.login.Take("login:"+c.ip, loginPerIP, loginWindow) {
		return nil, fail(http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	ck, err := c.r.Cookie(challengeCookie)
	if err != nil {
		return nil, fail(http.StatusUnauthorized, "Enter your password again.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSessions(c.now)
	i := slices.IndexFunc(s.st.Challenges, func(ch challenge) bool { return auth.Equal(ch.Hash, auth.Hash(ck.Value)) })
	if i < 0 || s.st.Account == nil || s.st.Account.MFA == nil || !s.st.Account.MFA.Active {
		return nil, fail(http.StatusUnauthorized, "Enter your password again.")
	}
	if until, locked := s.lockedUntil(c.now); locked {
		s.st.Challenges = slices.Delete(s.st.Challenges, i, i+1)
		_ = s.save()
		clearCookie(c.w, challengeCookie)
		return nil, lockedError(until)
	}
	with, ok := s.checkSecondFactor(in.Code, c.now)
	if !ok {
		s.audit(c, "login_fail", map[string]string{"reason": "wrong code"})
		if s.failAttempt(c, "mfa") {
			s.st.Challenges = slices.Delete(s.st.Challenges, i, i+1)
			_ = s.save()
			clearCookie(c.w, challengeCookie)
			until, _ := s.lockedUntil(c.now)
			return nil, lockedError(until)
		}
		return nil, fail(http.StatusUnauthorized, "Wrong code.")
	}
	s.st.Challenges = slices.Delete(s.st.Challenges, i, i+1)
	s.clearFailures()
	clearCookie(c.w, challengeCookie)
	csrf, err := s.openSession(c)
	if err != nil {
		return nil, err
	}
	s.audit(c, "login_ok", map[string]string{"with": with})
	return loginReply{Next: "done", CSRF: csrf}, nil
}

// checkSecondFactor accepts a TOTP code, once per time step, or an unused recovery code, which it uses
// up. Callers hold s.mu.
func (s *Server) checkSecondFactor(code string, now time.Time) (with string, ok bool) {
	m := s.st.Account.MFA
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) == 6 {
		key, err := s.mfaKey(m)
		if err != nil {
			return "", false
		}
		step, ok := auth.VerifyTOTP(key, code, now, m.LastStep)
		if ok {
			m.LastStep = step
			_ = s.save()
		}
		return "totp", ok
	}
	h := auth.Hash(normalizeRecovery(code))
	if i := slices.Index(m.Recovery, h); i >= 0 {
		m.Recovery = slices.Delete(m.Recovery, i, i+1)
		_ = s.save()
		return "recovery code", true
	}
	return "", false
}

func normalizeRecovery(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
}

func (s *Server) mfaKey(m *mfa) ([]byte, error) {
	if s.cfg.EncKey == nil {
		return nil, fail(http.StatusServiceUnavailable, "Two-step verification is not available: the server has no TODO_ENC_KEY.")
	}
	return auth.Open(s.cfg.EncKey, m.Secret, "account")
}

func (s *Server) postLogout(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.Sessions {
		if s.st.Sessions[i].Hash == c.session.Hash && s.st.Sessions[i].Ended.IsZero() {
			s.st.Sessions[i].Ended, s.st.Sessions[i].EndReason = c.now, "signed out"
		}
	}
	return s.afterEnding(c, "logout")
}

func (s *Server) getMe(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.st.Account.MFA
	type mfaInfo struct {
		On           bool `json:"on"`
		Pending      bool `json:"pending"`
		RecoveryLeft int  `json:"recoveryLeft"`
		Available    bool `json:"available"`
	}
	info := mfaInfo{Available: s.cfg.EncKey != nil}
	if m != nil {
		info.On, info.Pending = m.Active, !m.Active
		if m.Active {
			info.RecoveryLeft = len(m.Recovery)
		}
	}
	return map[string]any{"csrf": csrfFor(c.token), "mfa": info}, nil
}

// ---- account settings ----

func (s *Server) postPassword(c *call) (any, error) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	if err := auth.CheckPassword(in.New, passwordWords...); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if err := s.reauth(c, in.Current); err != nil {
		return nil, err
	}
	if in.Current == in.New {
		return nil, fail(http.StatusBadRequest, "The new password is the same as the current one.")
	}
	h, err := auth.HashPassword(in.New)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Account.Hash, s.st.Account.ChangedAt = h, c.now
	s.endSessions(c.now, "", "password changed") // this one too: it is replaced below
	csrf, err := s.openSession(c)
	if err != nil {
		return nil, err
	}
	s.audit(c, "password_changed", nil)
	return map[string]string{"csrf": csrf}, nil
}

// reauth checks the current password for a sensitive change. A wrong one counts toward the lock, and a
// lock it causes ends every session.
func (s *Server) reauth(c *call, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until, locked := s.lockedUntil(c.now); locked {
		return lockedError(until)
	}
	if ok, _ := auth.VerifyPassword(s.st.Account.Hash, password); ok {
		return nil
	}
	s.audit(c, "reauth_fail", nil)
	if s.failAttempt(c, "reauth") {
		s.endSessions(c.now, "", "locked")
		_ = s.save()
		clearCookie(c.w, sessionCookie)
		until, _ := s.lockedUntil(c.now)
		return lockedError(until)
	}
	return fail(http.StatusForbidden, "The current password is wrong.")
}

type sessionInfo struct {
	ID       string    `json:"id"`
	Current  bool      `json:"current"`
	Started  time.Time `json:"started"`
	LastUsed time.Time `json:"lastUsed"`
	IP       string    `json:"ip"`
	Agent    string    `json:"agent"`
}

func (s *Server) getSessions(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []sessionInfo{}
	for _, ss := range s.st.Sessions {
		if ss.Ended.IsZero() && c.now.Sub(ss.LastUsed) <= sessionIdle && c.now.Sub(ss.Started) <= sessionMax {
			out = append(out, sessionInfo{ss.ID, ss.Hash == c.session.Hash, ss.Started, ss.LastUsed, ss.IP, ss.Agent})
		}
	}
	return map[string]any{"sessions": out}, nil
}

func (s *Server) deleteSession(c *call) (any, error) {
	id := c.r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.Sessions {
		ss := &s.st.Sessions[i]
		if ss.ID == id && ss.Ended.IsZero() {
			ss.Ended, ss.EndReason = c.now, "ended from another session"
			if err := s.save(); err != nil {
				return nil, err
			}
			s.audit(c, "sessions_revoked", map[string]string{"count": "1"})
			if ss.Hash == c.session.Hash {
				clearCookie(c.w, sessionCookie)
			}
			return noContent{}, nil
		}
	}
	return nil, errNotFound
}

func (s *Server) postEndOthers(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.endSessions(c.now, c.session.Hash, "ended from another session")
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "sessions_revoked", map[string]string{"count": fmt.Sprint(n)})
	return map[string]int{"ended": n}, nil
}

// afterEnding saves, clears the cookie and records name, after the caller ended this session. Callers
// hold s.mu.
func (s *Server) afterEnding(c *call, name string) (any, error) {
	if err := s.save(); err != nil {
		return nil, err
	}
	clearCookie(c.w, sessionCookie)
	s.audit(c, name, nil)
	return noContent{}, nil
}

// ---- 2FA ----

func (s *Server) postMFAStart(c *call) (any, error) {
	if err := decode(c, &struct{}{}); err != nil {
		return nil, err
	}
	if s.cfg.EncKey == nil {
		return nil, fail(http.StatusServiceUnavailable, "Two-step verification is not available: the server has no TODO_ENC_KEY.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.st.Account.MFA; m != nil && m.Active {
		return nil, fail(http.StatusConflict, "Two-step verification is already on.")
	}
	secret := auth.NewTOTPSecret()
	sealed, err := auth.Seal(s.cfg.EncKey, secret, "account")
	if err != nil {
		return nil, err
	}
	s.st.Account.MFA = &mfa{Secret: sealed}
	if err := s.save(); err != nil {
		return nil, err
	}
	return map[string]string{"secret": auth.B32.EncodeToString(secret), "uri": auth.OTPAuthURI(appName, "to-do", secret)}, nil
}

func (s *Server) postMFAConfirm(c *call) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.st.Account.MFA
	if m == nil || m.Active {
		return nil, fail(http.StatusConflict, "Start turning on two-step verification again.")
	}
	key, err := s.mfaKey(m)
	if err != nil {
		return nil, err
	}
	step, ok := auth.VerifyTOTP(key, strings.TrimSpace(in.Code), c.now, 0)
	if !ok {
		if s.failAttempt(c, "mfa setup") {
			s.endSessions(c.now, "", "locked")
			_ = s.save()
			clearCookie(c.w, sessionCookie)
			until, _ := s.lockedUntil(c.now)
			return nil, lockedError(until)
		}
		return nil, fail(http.StatusBadRequest, "Wrong code. Check the time on your phone and try the next one.")
	}
	codes := make([]string, 10)
	m.Recovery = make([]string, 10)
	for i := range codes {
		raw := strings.ToLower(auth.B32.EncodeToString(auth.NewTOTPSecret()))[:10]
		codes[i] = raw[:5] + "-" + raw[5:]
		m.Recovery[i] = auth.Hash(raw)
	}
	m.Active, m.LastStep, m.EnabledAt = true, step, c.now
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "mfa_on", nil)
	return map[string][]string{"recoveryCodes": codes}, nil
}

func (s *Server) postMFADisable(c *call) (any, error) {
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
	s.st.Account.MFA = nil
	s.endSessions(c.now, "", "2FA turned off")
	return s.afterEnding(c, "mfa_off")
}

// ---- devices ----

type deviceInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

func (s *Server) getDevices(c *call) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []deviceInfo{}
	for _, d := range s.st.Devices {
		out = append(out, deviceInfo{d.ID, d.Name, d.Created, d.LastUsed})
	}
	return map[string]any{"devices": out}, nil
}

func (s *Server) postDevice(c *call) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" || len([]rune(name)) > 60 {
		return nil, fail(http.StatusBadRequest, "Give the computer a name of up to 60 characters.")
	}
	token := "ckt_" + auth.RandomToken()
	d := device{Hash: auth.Hash(token), ID: auth.RandomToken()[:16], Name: name, Created: c.now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.st.Devices) >= 20 {
		return nil, fail(http.StatusConflict, "Too many computers connected. Revoke one first.")
	}
	s.st.Devices = append(s.st.Devices, d)
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "device_added", map[string]string{"device": d.ID, "name": name})
	return map[string]any{"device": deviceInfo{d.ID, d.Name, d.Created, d.LastUsed}, "token": token}, nil
}

func (s *Server) deleteDevice(c *call) (any, error) {
	id := c.r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.st.Devices, func(d device) bool { return d.ID == id })
	if i < 0 {
		return nil, errNotFound
	}
	name := s.st.Devices[i].Name
	s.st.Devices = slices.Delete(s.st.Devices, i, i+1)
	if err := s.save(); err != nil {
		return nil, err
	}
	s.audit(c, "device_revoked", map[string]string{"device": id, "name": name})
	return noContent{}, nil
}

func (s *Server) getAudit(c *call) (any, error) {
	events, err := lastEvents(s.paths.audit(), 100)
	if err != nil {
		return nil, err
	}
	return map[string]any{"events": events}, nil
}

// ---- the server's command line ----

// SetPassword creates the account or replaces its password, from the server's command line: it ends
// every session and clears the lock, and leaves 2FA as it was.
func (s *Server) SetPassword(password string) error {
	if err := auth.CheckPassword(password, passwordWords...); err != nil {
		return err
	}
	h, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	created := s.st.Account == nil
	if created {
		s.st.Account = &account{}
	}
	s.st.Account.Hash, s.st.Account.ChangedAt = h, now
	s.clearFailures()
	n := s.endSessions(now, "", "password reset on the server")
	if err := s.save(); err != nil {
		return err
	}
	name := "password_reset"
	if created {
		name = "account_created"
	}
	return s.AuditCLI(name, map[string]string{"sessionsEnded": fmt.Sprint(n)})
}

// ResetMFA turns 2FA off and removes every passkey, from the server's command line: for a lost phone
// without recovery codes.
func (s *Server) ResetMFA() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Account == nil || (s.st.Account.MFA == nil && len(s.st.Passkeys) == 0) {
		return fmt.Errorf("neither two-step verification nor a passkey is on")
	}
	s.st.Account.MFA, s.st.Passkeys = nil, nil
	n := s.endSessions(s.cfg.Now(), "", "2FA reset on the server")
	if err := s.save(); err != nil {
		return err
	}
	return s.AuditCLI("mfa_off", map[string]string{"sessionsEnded": fmt.Sprint(n), "by": "server"})
}
