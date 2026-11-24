package todoserver

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

func TestTasksCRUD(t *testing.T) {
	e := newEnv(t).withAccount()
	c := e.client()
	c.signIn()
	r := c.do("POST", "/api/tasks", map[string]string{"line": "call the venue #Work @tomorrow"})
	c.expect(r, 201, "add")
	task := r.body["task"].(map[string]any)
	id := task["id"].(string)
	if task["text"] != "call the venue" || task["group"] != "Work" || task["due"] != "2026-11-25" {
		t.Errorf("parsed: %v", task)
	}
	c.do("POST", "/api/tasks", map[string]string{"line": "plain one"})

	r = c.do("GET", "/api/tasks", nil)
	if r.body["defaultGroup"] != "Personal" || len(r.body["tasks"].([]any)) != 2 {
		t.Errorf("list: %s", r.Body)
	}
	if g := r.body["groups"].([]any); len(g) != 1 || g[0] != "Work" {
		t.Errorf("groups: %v", g)
	}

	r = c.do("PATCH", "/api/tasks/"+id, map[string]string{"line": "call the venue again @today"})
	if tk := r.body["task"].(map[string]any); tk["text"] != "call the venue again" || tk["group"] != nil || tk["due"] != "2026-11-24" {
		t.Errorf("edit: %s", r.Body)
	}
	r = c.do("PATCH", "/api/tasks/"+id, map[string]any{"done": true})
	if r.body["task"].(map[string]any)["done"] == nil {
		t.Errorf("complete: %s", r.Body)
	}
	tasks := c.do("GET", "/api/tasks", nil).body["tasks"].([]any)
	if tasks[len(tasks)-1].(map[string]any)["id"] != id {
		t.Error("a completed task is not listed after the open ones")
	}
	c.expect(c.do("PATCH", "/api/tasks/"+id, map[string]any{"done": false}), 200, "reopen")
	c.expect(c.do("PATCH", "/api/tasks/"+id, map[string]any{"done": true, "line": "x"}), 400, "both line and done")
	c.expect(c.do("PATCH", "/api/tasks/"+id, map[string]any{}), 400, "neither")
	c.expect(c.do("DELETE", "/api/tasks/"+id, "{}"), 204, "delete")
	c.expect(c.do("DELETE", "/api/tasks/"+id, "{}"), 404, "delete again")
	c.expect(c.do("PATCH", "/api/tasks/"+id, map[string]any{"done": true}), 404, "change a deleted task")
	if strings.Contains(c.do("GET", "/api/tasks", nil).Body.String(), id) {
		t.Error("a deleted task is listed")
	}
}

func TestSync(t *testing.T) {
	e := newEnv(t).withAccount()
	phone := e.client()
	phone.signIn()
	token := phone.do("POST", "/api/devices", map[string]string{"name": "Mac"}).str("token")
	onPhone := phone.do("POST", "/api/tasks", map[string]string{"line": "from the phone"}).body["task"].(map[string]any)["id"].(string)

	mac := e.client()
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	t0 := e.now
	fromMac := todo.Task{ID: "tmac1", Text: "from the mac", Created: t0, Updated: t0}
	r := mac.do("POST", "/api/sync", map[string]any{"tasks": []todo.Task{fromMac}}, bearer)
	mac.expect(r, 200, "sync")
	if n := len(r.body["tasks"].([]any)); n != 2 {
		t.Errorf("merged list has %d tasks: %s", n, r.Body)
	}

	// A deletion on the mac reaches the phone; an older edit does not undo a newer one.
	gone := fromMac
	gone.Deleted, gone.Updated = t0.Add(time.Minute), t0.Add(time.Minute)
	stale := todo.Task{ID: onPhone, Text: "stale edit", Created: t0.Add(-time.Hour), Updated: t0.Add(-time.Hour)}
	mac.expect(mac.do("POST", "/api/sync", map[string]any{"tasks": []todo.Task{gone, stale}}, bearer), 200, "sync a deletion")
	list := phone.do("GET", "/api/tasks", nil).Body.String()
	if strings.Contains(list, "from the mac") || strings.Contains(list, "stale edit") || !strings.Contains(list, "from the phone") {
		t.Errorf("phone's list after sync: %s", list)
	}

	for name, bad := range map[string]todo.Task{
		"no id":     {Text: "x", Updated: t0},
		"long id":   {ID: strings.Repeat("a", 65), Updated: t0},
		"id spaces": {ID: "a b", Updated: t0},
		"future":    {ID: "tf", Text: "x", Updated: t0.Add(48 * time.Hour)},
		"bad due":   {ID: "td", Text: "x", Due: "tomorrow", Updated: t0},
		"long text": {ID: "tl", Text: strings.Repeat("x", maxText+1), Updated: t0},
	} {
		mac.expect(mac.do("POST", "/api/sync", map[string]any{"tasks": []todo.Task{bad}}, bearer), 400, "sync a task with "+name)
	}

	phone.expect(phone.do("DELETE", "/api/devices/"+phone.do("GET", "/api/devices", nil).body["devices"].([]any)[0].(map[string]any)["id"].(string), "{}"), 204, "revoke")
	mac.expect(mac.do("POST", "/api/sync", map[string]any{"tasks": []todo.Task{}}, bearer), 401, "sync after the revoke")
	if strings.Contains(readAll(t, e.s.paths.state()), token) {
		t.Error("the device token is stored as is")
	}
}
