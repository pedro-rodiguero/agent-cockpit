package todoserver

import (
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Limits of what the list accepts: a task is a line, not a document.
const (
	maxLine     = 500
	maxText     = 1000
	maxSyncTask = 5000
	recentDone  = 7 * 24 * time.Hour
)

func (s *Server) getTasks(c *call) (any, error) {
	l, err := s.tasks.Load()
	if err != nil {
		return nil, err
	}
	return s.listReply(l, c.now), nil
}

// listReply is the list as the app shows it: open tasks in the dashboard's order (by group, then due
// date), then those completed in the last week, latest first.
func (s *Server) listReply(l todo.List, now time.Time) map[string]any {
	tasks := []todo.Task{}
	for _, sh := range l.Open(false) {
		tasks = append(tasks, sh.Task)
	}
	var done []todo.Task
	for _, t := range l.Tasks {
		if t.Live() && !t.Open() && now.Sub(t.Done) <= recentDone {
			done = append(done, t)
		}
	}
	slices.SortStableFunc(done, func(a, b todo.Task) int { return b.Done.Compare(a.Done) })
	groups := []string{}
	for _, g := range l.Groups() {
		if g != l.DefaultGroup() {
			groups = append(groups, g)
		}
	}
	return map[string]any{"tasks": append(tasks, done...), "groups": groups, "defaultGroup": s.cfg.DefaultGroup}
}

// parseLine reads the quick-add syntax, refusing what is not a task.
func parseLine(line string, l *todo.List, now time.Time) (text, group, due string, err error) {
	if !utf8.ValidString(line) || len([]rune(line)) > maxLine {
		return "", "", "", fail(http.StatusBadRequest, "A task is one line of up to 500 characters.")
	}
	line = strings.Join(strings.Fields(line), " ")
	text, group, due = todo.Parse(line, l.Groups(), now)
	if text == "" {
		return "", "", "", fail(http.StatusBadRequest, "The task has no text.")
	}
	return text, group, due, nil
}

func (s *Server) postTask(c *call) (any, error) {
	var in struct {
		Line string `json:"line"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	var added todo.Task
	var perr error
	err := s.tasks.Update(c.now, func(l *todo.List) {
		text, group, due, err := parseLine(in.Line, l, c.now)
		if err != nil {
			perr = err
			return
		}
		added = l.Add(text, group, due, c.now)
	})
	if err != nil {
		return nil, err
	}
	if perr != nil {
		return nil, perr
	}
	return map[string]todo.Task{"task": added}, nil
}

func (s *Server) patchTask(c *call) (any, error) {
	var in struct {
		Line *string `json:"line"`
		Done *bool   `json:"done"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	if (in.Line == nil) == (in.Done == nil) {
		return nil, fail(http.StatusBadRequest, "Send either line or done.")
	}
	id := c.r.PathValue("id")
	var out todo.Task
	var perr error
	err := s.tasks.Update(c.now, func(l *todo.List) {
		if _, ok := l.Find(id); !ok {
			perr = errNotFound
			return
		}
		switch {
		case in.Line != nil:
			text, group, due, err := parseLine(*in.Line, l, c.now)
			if err != nil {
				perr = err
				return
			}
			l.Edit(id, text, group, due, c.now)
		case *in.Done:
			l.Complete(id, c.now)
		default:
			l.Reopen(id, c.now)
		}
		out, _ = l.Find(id)
	})
	if err != nil {
		return nil, err
	}
	if perr != nil {
		return nil, perr
	}
	return map[string]todo.Task{"task": out}, nil
}

func (s *Server) deleteTask(c *call) (any, error) {
	id := c.r.PathValue("id")
	found := false
	if err := s.tasks.Update(c.now, func(l *todo.List) { found = l.Delete(id, c.now) }); err != nil {
		return nil, err
	}
	if !found {
		return nil, errNotFound
	}
	return noContent{}, nil
}

// postSync merges a computer's tasks into the list and answers with the whole list, tombstones
// included, for the computer to merge back.
func (s *Server) postSync(c *call) (any, error) {
	var in struct {
		Tasks []todo.Task `json:"tasks"`
	}
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	if len(in.Tasks) > maxSyncTask {
		return nil, fail(http.StatusRequestEntityTooLarge, "Too many tasks.")
	}
	for _, t := range in.Tasks {
		if err := validTask(t, c.now); err != nil {
			return nil, err
		}
	}
	var all []todo.Task
	err := s.tasks.Update(c.now, func(l *todo.List) {
		l.Merge(in.Tasks)
		all = append([]todo.Task{}, l.Tasks...)
	})
	if err != nil {
		return nil, err
	}
	return map[string][]todo.Task{"tasks": all}, nil
}

// validTask refuses a synced task no client of the list would write: it guards the list against a
// device token in the wrong hands, or a client with a bug, filling it with junk.
func validTask(t todo.Task, now time.Time) error {
	bad := func(why string) error { return fail(http.StatusBadRequest, "Task "+truncate(t.ID, 40)+": "+why) }
	switch {
	case t.ID == "" || len(t.ID) > 64 || strings.ContainsFunc(t.ID, func(r rune) bool { return r < '!' || r > '~' }):
		return bad("bad id")
	case !utf8.ValidString(t.Text) || len([]rune(t.Text)) > maxText || !utf8.ValidString(t.Group) || len([]rune(t.Group)) > 100:
		return bad("text or group too long")
	case t.Due != "":
		if _, err := time.Parse(todo.DateLayout, t.Due); err != nil {
			return bad("bad due date")
		}
	}
	if t.Updated.After(now.Add(24 * time.Hour)) {
		return bad("changed in the future") // would win every merge from then on
	}
	return nil
}
