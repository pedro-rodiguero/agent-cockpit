package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// Agy (Antigravity CLI) keeps each conversation in conversations/<id>.db (SQLite with protobuf
// payloads). It writes no plain-text status, so the state is inferred: if the database the process holds
// open was written within BusyWindow the session is busy, otherwise idle. Its sessions are marked
// Heuristic, and it never reports waiting.
type Agy struct {
	Root       string        // ~/.gemini/antigravity-cli
	BusyWindow time.Duration // default 20s
}

func (a *Agy) Name() string { return "agy" }

func (a *Agy) Collect(ctx context.Context, env *Env) ([]model.Session, error) {
	window := a.BusyWindow
	if window == 0 {
		window = 20 * time.Second
	}
	convDir := filepath.Join(a.Root, "conversations")
	var out []model.Session
	for _, p := range env.Procs {
		if p.Cmd != "agy" || p.TTY == "" {
			continue
		}
		if parent, ok := env.Procs[p.PPID]; ok && parent.Cmd == "agy" {
			continue
		}
		cwd := procCwd(ctx, p.PID)
		s := model.Session{
			ID:        "agy:" + strconv.Itoa(p.PID),
			Agent:     "agy",
			Name:      filepath.Base(cwd),
			Cwd:       cwd,
			PID:       p.PID,
			TTY:       p.TTY,
			State:     model.Idle,
			Heuristic: true,
		}
		var newest time.Time
		for _, n := range lsofNames(ctx, p.PID, false) {
			if !strings.HasPrefix(n, convDir) {
				continue
			}
			base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, "-wal"), "-shm"), ".db")
			if fi, err := os.Stat(n); err == nil && fi.ModTime().After(newest) {
				newest = fi.ModTime()
				s.Log = base + ".db"
			}
		}
		s.LastActivity = newest
		s.Since = newest
		if !newest.IsZero() && env.Now.Sub(newest) < window {
			s.State = model.Busy
		}
		out = append(out, s)
	}
	return out, nil
}
