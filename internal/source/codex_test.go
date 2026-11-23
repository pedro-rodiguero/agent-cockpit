package source

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestParseRollout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-11-19T14-20-24-01a1f4d2-e300-7363-acd2-0b0adfce6afc.jsonl")
	lines := []string{
		`{"type":"session_meta","payload":{"cwd":"/src/web"}}`,
		`{"timestamp":"2026-11-19T14:20:30Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-11-19T14:20:31Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"fix the"},{"text":"build"}]}}}`,
		`{"timestamp":"2026-11-19T14:21:00Z","type":"event_msg","payload":{"type":"exec_approval_request"}}`,
	}
	write(t, path, strings.Join(lines, "\n")+"\n")
	st := parseRollout(path)
	if st.state != model.Waiting || st.detail != "exec approval request" || st.lastPrompt != "fix the build" {
		t.Fatalf("waiting turn: %+v", st)
	}

	lines = append(lines, `{"timestamp":"2026-11-19T14:22:00Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Build fixed."}}`)
	write(t, path, strings.Join(lines, "\n")+"\n")
	st = parseRollout(path)
	if st.state != model.Idle || st.title != "Build fixed." {
		t.Fatalf("completed turn: %+v", st)
	}
	if id := rolloutID(path); id != "01a1f4d2-e300-7363-acd2-0b0adfce6afc" {
		t.Errorf("rolloutID = %q", id)
	}
}
