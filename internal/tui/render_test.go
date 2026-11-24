package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
)

// COCKPIT_RENDER=out.ans [COCKPIT_RENDER_SIZE=160x44] [COCKPIT_SPAWN=claude:/dir] go test ./internal/tui -run TestRender
// writes one grid frame and one list frame with live data, optionally after opening a pane in an
// already running pane server. Colors follow the terminal, as on screen; COCKPIT_COLOR forces a depth.
func TestRender(t *testing.T) {
	out := os.Getenv("COCKPIT_RENDER")
	if out == "" {
		t.Skip("COCKPIT_RENDER not set")
	}
	a := newApp(source.Default(), Options{Grid: true})
	a.w, a.h = 204, 54
	if size := os.Getenv("COCKPIT_RENDER_SIZE"); size != "" {
		if _, err := fmt.Sscanf(size, "%dx%d", &a.w, &a.h); err != nil {
			t.Fatalf("COCKPIT_RENDER_SIZE %q: want COLSxROWS", size)
		}
	}
	// Attach only to a server that is already running: the test must not start one.
	mc, err := mux.Connect()
	if err == nil {
		defer mc.Close()
		a.mc = mc
		go mc.Run()
		time.Sleep(300 * time.Millisecond)
	} else {
		t.Logf("%v: rendering without panes", err)
	}
	collect := func() {
		snap := a.col.Collect(context.Background())
		a.convs = readChats(&a.chats, snap.Sessions)
		a.snap = markAsked(snap, a.convs)
		a.loading = false
		a.rebuild()
		a.relayout() // sends pane resizes, as the event loop does
	}
	if spec := os.Getenv("COCKPIT_SPAWN"); spec != "" && mc != nil {
		agent, dir, _ := strings.Cut(spec, ":")
		collect()
		cols, rows := a.newPaneSize()
		_ = mc.Send(mux.Msg{Op: mux.OpSpawn, Agent: agent, Cwd: dir, Cols: cols, Rows: rows})
		time.Sleep(9 * time.Second)
	}
	collect()
	time.Sleep(1500 * time.Millisecond) // let resized panes redraw
	a.rebuild()
	if len(a.tiles) > 0 {
		a.sel = a.tiles[0].key
	}
	a.relayout()
	grid, _ := a.compose()
	a.view = listView
	a.rebuild()
	a.relayout()
	list, _ := a.compose()
	frame := strings.Join(grid, "\x1b[0m\n") + "\x1b[0m\n\n" + strings.Join(list, "\x1b[0m\n") + "\x1b[0m"
	if detectColorDepth(os.Getenv) == color256 {
		frame = newDownsampler().line(frame)
	}
	if err := os.WriteFile(out, []byte(frame), 0o644); err != nil {
		t.Fatal(err)
	}
}
