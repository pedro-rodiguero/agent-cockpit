package source

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// Source is one adapter: it finds the live sessions of one tool and reports their state. Supporting a new
// agent means adding a Source here; anything else can be plugged in from outside as an External.
type Source interface {
	Name() string
	Collect(ctx context.Context, env *Env) ([]model.Session, error)
}

// Env is shared by all sources during one collection pass, so the process table is read once.
type Env struct {
	Procs map[int]Proc
	Now   time.Time
}

// Collector runs its sources in parallel and merges the results into one Snapshot.
type Collector struct {
	Sources []Source
	// StuckAfter turns a busy session into a stuck one when its log has been quiet for this long.
	StuckAfter time.Duration
}

func (c *Collector) Collect(ctx context.Context) model.Snapshot {
	snap := model.Snapshot{At: time.Now(), Errors: map[string]string{}}
	procs, err := listProcs(ctx)
	if err != nil {
		snap.Errors["ps"] = err.Error()
	}
	env := &Env{Procs: procs, Now: snap.At}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range c.Sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			got, err := s.Collect(sctx, env)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				snap.Errors[s.Name()] = err.Error()
			}
			snap.Sessions = append(snap.Sessions, got...)
		}(s)
	}
	wg.Wait()

	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		if s.State == model.Busy && c.StuckAfter > 0 && !s.LastActivity.IsZero() && snap.At.Sub(s.LastActivity) > c.StuckAfter {
			s.State = model.Stuck
			s.Detail = "no activity for " + humanDur(snap.At.Sub(s.LastActivity))
			s.Since = s.LastActivity
		}
	}
	sort.SliceStable(snap.Sessions, func(i, j int) bool {
		a, b := snap.Sessions[i], snap.Sessions[j]
		if a.State.Rank() != b.State.Rank() {
			return a.State.Rank() < b.State.Rank()
		}
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		if a.State == model.Todo {
			return false // a to-do list keeps the order its source gave it
		}
		return a.Name < b.Name
	})
	if len(snap.Errors) == 0 {
		snap.Errors = nil
	}
	return snap
}

func humanDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return d.String()
	case d < time.Hour:
		return d.Truncate(time.Minute).String()
	default:
		return strings.TrimSuffix(d.Truncate(time.Minute).String(), "0s")
	}
}

// clip flattens whitespace and truncates s to max runes.
func clip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// msTime converts a Unix time in milliseconds, keeping 0 as the zero time.
func msTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
