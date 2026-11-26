package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestExternal(t *testing.T) {
	bin := t.TempDir()
	script := func(name, body string) { executable(t, filepath.Join(bin, name), body) }
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
	executable(t, path, "test -f \"$0.down\" && exit 1\necho '[{\"id\":\"a\"}]'")
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

// testCtx ends with the test or after 10 seconds, so a source that hangs fails the test instead of
// stalling the run.
func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// executable writes a script and launches it once, so the tests time the source rather than the first
// launch of a new file. macOS vets that first launch, which on a loaded machine took seconds and now and
// then never finished: the process stayed at _dyld_start, for hours once, while the same file started
// at once later. A launch that does not finish is killed and tried again with a new process.
func executable(t *testing.T, path, body string) {
	t.Helper()
	write(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, path)
		cmd.WaitDelay = time.Second
		_ = cmd.Run()
		late := ctx.Err() != nil
		cancel()
		if !late {
			return
		}
		if attempt == 4 {
			t.Fatalf("%s never finished starting", filepath.Base(path))
		}
	}
}
