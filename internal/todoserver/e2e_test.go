package todoserver

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todosync"
)

// A computer connected with a device token and the phone's app see the same list.
func TestComputerAndPhoneShareTheList(t *testing.T) {
	e := newEnv(t).withAccount()
	phone := e.client()
	phone.signIn()
	token := phone.do("POST", "/api/devices", map[string]string{"name": "Mac"}).str("token")
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	mac := &todo.Store{Path: filepath.Join(t.TempDir(), "todo.json")}
	_ = mac.Update(e.now, func(l *todo.List) { l.Add("buy stamps", "Errands", "", e.now) })
	link := todosync.Link{URL: srv.URL, Token: token}
	if err := todosync.Sync(context.Background(), link, mac, e.now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(phone.do("GET", "/api/tasks", nil).Body.String(), "buy stamps") {
		t.Fatal("the phone does not see the computer's task")
	}

	phone.do("POST", "/api/tasks", map[string]string{"line": "call the venue @tomorrow"})
	if err := todosync.Sync(context.Background(), link, mac, e.now); err != nil {
		t.Fatal(err)
	}
	l, _ := mac.Load()
	if len(l.Open(false)) != 2 {
		t.Errorf("the computer does not see the phone's task: %+v", l.Tasks)
	}
}
