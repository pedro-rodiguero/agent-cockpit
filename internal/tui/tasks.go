package tui

import (
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Tasks in cockpit's own to-do list are edited from the rail: + adds, space completes, u undoes the
// last completion, e edits and x deletes. Tasks from external sources are only listed; ⏎ opens them.

// ownTask reports whether the tile is a task of cockpit's own to-do list.
func (t tile) ownTask() bool { return t.isTask() && t.sess.Agent == source.TasksName }

// taskID is the to-do list's ID of an own task.
func (t tile) taskID() string { return strings.TrimPrefix(t.sess.ID, source.TasksName+":") }

// promptNewTask asks for a task. Without #group it joins the selected task's group, so adding next to
// a task adds to its section.
func (a *App) promptNewTask() {
	pr := &prompt{kind: promptTask, label: "new task"}
	if t, ok := a.selected(); ok && t.ownTask() {
		if task, ok := a.findTask(t.taskID()); ok && task.Group != "" {
			pr.group = task.Group
			pr.label += " in " + task.Group
		}
	}
	a.openPrompt(pr)
}

func (a *App) promptEditTask() {
	t, ok := a.selected()
	if !ok || !t.ownTask() {
		a.say(notOwnTask(t))
		return
	}
	task, ok := a.findTask(t.taskID())
	if !ok {
		return
	}
	a.openPrompt(&prompt{kind: promptTask, label: "edit task", text: []rune(todo.Format(task)), taskID: task.ID})
}

// saveTask adds the typed task, in group unless it names one, or changes the one being edited.
func (a *App) saveTask(id, group, line string) {
	now := time.Now()
	err := a.tasks.Update(now, func(l *todo.List) {
		text, typedGroup, due := todo.Parse(line, l.Groups(), now)
		if text == "" {
			return
		}
		if id == "" {
			l.Add(text, firstOf(typedGroup, group), due, now)
		} else {
			l.Edit(id, text, typedGroup, due, now)
		}
	})
	a.afterTaskChange(err, "")
}

func (a *App) completeTask() {
	t, ok := a.selected()
	if !ok || !t.ownTask() {
		a.say(notOwnTask(t))
		return
	}
	id, name := t.taskID(), t.name()
	err := a.tasks.Update(time.Now(), func(l *todo.List) { l.Complete(id, time.Now()) })
	if err == nil {
		a.lastDone = id
		a.dropSession(t.sess.ID)
	}
	a.afterTaskChange(err, "done: "+name+" · u undoes")
}

func (a *App) undoComplete() {
	if a.lastDone == "" {
		a.say("nothing to undo")
		return
	}
	id := a.lastDone
	a.lastDone = ""
	err := a.tasks.Update(time.Now(), func(l *todo.List) { l.Reopen(id, time.Now()) })
	a.afterTaskChange(err, "reopened")
}

func (a *App) confirmDeleteTask(t tile) {
	id, name, sid := t.taskID(), t.name(), t.sess.ID
	a.confirm("delete "+name+"? · y·⏎ yes · n·esc no", func() {
		err := a.tasks.Update(time.Now(), func(l *todo.List) { l.Delete(id, time.Now()) })
		if err == nil {
			a.dropSession(sid)
		}
		a.afterTaskChange(err, "deleted "+name)
	})
}

func (a *App) findTask(id string) (todo.Task, bool) {
	l, err := a.tasks.Load()
	if err != nil {
		a.say("to-do list: " + err.Error())
		return todo.Task{}, false
	}
	return l.Find(id)
}

// afterTaskChange reports the outcome and collects again at once, so the rail shows the change now
// rather than on the next pass.
func (a *App) afterTaskChange(err error, ok string) {
	if err != nil {
		a.say("to-do list: " + err.Error())
		return
	}
	if ok != "" {
		a.say(ok)
	}
	select {
	case a.collectNow <- struct{}{}:
	default:
	}
}

// dropSession removes a session from the current snapshot ahead of the next collection pass.
func (a *App) dropSession(id string) {
	kept := a.snap.Sessions[:0:0]
	for _, s := range a.snap.Sessions {
		if s.ID != id {
			kept = append(kept, s)
		}
	}
	a.snap.Sessions = kept
	a.rebuild()
}

func notOwnTask(t tile) string {
	if t.isTask() {
		return "this task belongs to " + t.sess.Agent + " · ⏎ opens it there"
	}
	return "select a task in the rail · + adds one"
}
