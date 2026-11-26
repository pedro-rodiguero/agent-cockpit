package quick

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOutputFeedsStdin(t *testing.T) {
	out, err := Output("hello", "cat")
	if err != nil || string(out) != "hello" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestOutputKillsAHelperThatHangs(t *testing.T) {
	start := time.Now()
	_, err := Output("", "/bin/sh", "-c", "sleep 60")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > Timeout+3*time.Second {
		t.Fatalf("took %v", d)
	}
}
