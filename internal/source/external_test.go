package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestExternal(t *testing.T) {
	bin := t.TempDir()
	script := func(name, body string) {
		write(t, filepath.Join(bin, name), "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("cockpit-source-reviews", `echo '[{"id":"pr-7","name":"web","detail":"review requested","quiet":true},{"name":"no id"}]'`)
	script("cockpit-source-broken", `echo "token expired" >&2; exit 1`)
	write(t, filepath.Join(bin, "cockpit-source-not-executable"), "")
	write(t, filepath.Join(bin, "unrelated"), "")

	found := FindExternal(bin)
	var names []string
	for _, e := range found {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "broken,reviews" {
		t.Fatalf("FindExternal = %s", got)
	}

	now := time.Now()
	got, err := found[1].Collect(testCtx(t), &Env{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "reviews:pr-7" || got[0].Agent != "reviews" || got[0].State != model.Inbox || !got[0].Quiet {
		t.Fatalf("first item: %+v", got)
	}
	if got[1].ID != "reviews:1" {
		t.Errorf("generated id = %q", got[1].ID)
	}

	_, err = found[0].Collect(testCtx(t), &Env{Now: now})
	if err == nil || err.Error() != "token expired" {
		t.Errorf("failing source error = %v", err)
	}
}

func TestExternalKeepsLastResultWhileFailing(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "cockpit-source-flaky")
	write(t, path, "#!/bin/sh\ntest -f \"$0.down\" && exit 1\necho '[{\"id\":\"a\"}]'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	e := &External{Path: path, Every: time.Second, Keep: time.Minute}
	t0 := time.Now()
	if got, _ := e.Collect(testCtx(t), &Env{Now: t0}); len(got) != 1 {
		t.Fatalf("first run: %v", got)
	}
	write(t, path+".down", "")
	if got, err := e.Collect(testCtx(t), &Env{Now: t0.Add(2 * time.Second)}); len(got) != 1 || err == nil {
		t.Fatalf("while failing: %v, %v", got, err)
	}
	if got, _ := e.Collect(testCtx(t), &Env{Now: t0.Add(2 * time.Minute)}); len(got) != 0 {
		t.Fatalf("after Keep: %v", got)
	}
}

// testCtx ends with the test or after a minute, so a source that hangs fails the test instead of
// stalling it. Not less: macOS scans an executable the first time it runs, and on a loaded machine the
// first run of a script written a moment ago has taken over 5 seconds.
func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}
