// Package peer sends a message into a running interactive Claude Code session, as if the person had
// typed it, through the messaging socket the session advertises in <config>/sessions/<pid>.json.
//
// This is the mechanism Claude Code sessions use to message each other. It is not a documented public
// API, so it may change between Claude Code releases; docs/compatibility.md records what cockpit relies
// on. The frames are newline-delimited JSON:
//
//  1. {"type":"auth","token":...} when the session wrote a key file for its socket
//     (<sessions>/<pid>.<sha256 of the socket's absolute path>.key);
//  2. a "user" frame whose content is wrapped in <cross-session-message from-name=...>.
package peer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var closeTag = regexp.MustCompile(`(?i)<(\s*/\s*cross-session-message)`)

// Send queues text as the next user turn of the session. sessionsDir is <claude config>/sessions.
//
// The envelope names the sender "cockpit" with from-mode="bypass": the text comes straight from the
// person at the keyboard, so a session running with bypassed permissions should not hold it for a
// separate approval. Any closing tag inside text is defused so it cannot end the envelope early.
func Send(sessionsDir string, pid int, sessionID, socket, text string) error {
	if socket == "" {
		return fmt.Errorf("session has no messaging socket")
	}
	var frames []any
	if tok := token(sessionsDir, pid, socket); tok != "" {
		frames = append(frames, map[string]any{"type": "auth", "token": tok})
	}
	body := closeTag.ReplaceAllString(text, `<\$1`)
	content := "<cross-session-message from-name=\"cockpit\" from-mode=\"bypass\">\n" + body + "\n</cross-session-message>"
	frames = append(frames, map[string]any{
		"msgV":       1,
		"msg_id":     uuid(),
		"type":       "user",
		"message":    map[string]any{"role": "user", "content": content},
		"priority":   "next",
		"session_id": sessionID,
	})
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var sb strings.Builder
	for _, f := range frames {
		b, _ := json.Marshal(f)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		return err
	}
	// Half-close only after the session has had a moment to read: it handles EOF by tearing the
	// connection down, and frames still unread at that point can be dropped.
	time.Sleep(150 * time.Millisecond)
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	return nil
}

// token reads the peer token the session stored for its socket, or "" when it requires none.
func token(dir string, pid int, socket string) string {
	abs, _ := filepath.Abs(socket)
	sum := sha256.Sum256([]byte(abs))
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.%s.key", pid, hex.EncodeToString(sum[:]))))
	if err != nil {
		return ""
	}
	var k struct {
		PeerToken string `json:"peerToken"`
	}
	_ = json.Unmarshal(b, &k)
	return k.PeerToken
}

// uuid returns a random version 4 UUID.
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
