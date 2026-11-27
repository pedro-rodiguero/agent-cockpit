package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWrapWideCharacters(t *testing.T) {
	cases := []struct {
		s    string
		w    int
		want []string
	}{
		{"日本語", 1, []string{"日", "本", "語"}},
		{"日本語", 4, []string{"日本", "語"}},
		{"a 日本", 3, []string{"a", "日", "本"}},
		{"hello world", 5, []string{"hello", "world"}},
		{"abcdefgh", 3, []string{"abc", "def", "gh"}},
		{"x", 0, nil},
	}
	for _, c := range cases {
		if got := wrap(c.s, c.w); !reflect.DeepEqual(got, c.want) {
			t.Errorf("wrap(%q, %d) = %q, want %q", c.s, c.w, got, c.want)
		}
	}
}

func TestFitIsExactWidth(t *testing.T) {
	for _, s := range []string{"", "abc", "日本語", "Área de trabalho", "a\tb\nc"} {
		for w := 1; w <= 8; w++ {
			if got := width(fit(s, w)); got != w {
				t.Errorf("width(fit(%q, %d)) = %d", s, w, got)
			}
		}
	}
	if got := fit("日本語", 5); got != "日本…" {
		t.Errorf("fit cut = %q", got)
	}
	if got := fitLeft("/a/b/project", 8); got != "…project" {
		t.Errorf("fitLeft = %q", got)
	}
}

func TestTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	cases := map[string]string{
		home:                   "~",
		home + "/src":          "~/src",
		home + "x/src":         home + "x/src",
		"/elsewhere":           "/elsewhere",
		filepath.Dir(home):     filepath.Dir(home),
		home + "/Área/projeto": "~/Área/projeto",
	}
	for in, want := range cases {
		if got := tilde(in); got != want {
			t.Errorf("tilde(%q) = %q, want %q", in, got, want)
		}
		if got := untilde(tilde(in)); got != in {
			t.Errorf("untilde(tilde(%q)) = %q", in, got)
		}
	}
}

func TestCompleteDirMultibyte(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Área1", "Área2", "Às", ".hidden", "plain"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Áfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		root + "/Á":  root + "/Área", // common prefix, cut on a character boundary
		root + "/À":  root + "/Às/",  // single match gets a slash
		root + "/":   root + "/",     // Á and À share a byte, not a character
		root + "/.h": root + "/.hidden/",
		root + "/p":  root + "/plain/",
		root + "/zz": root + "/zz",
	}
	for in, want := range cases {
		if got := completeDir(in); got != want {
			t.Errorf("completeDir(%q) = %q, want %q", in, got, want)
		}
	}
	for in := range cases {
		if got := completeDir(in); !strings.HasPrefix(got, root) || !isValidUTF8(got) {
			t.Errorf("completeDir(%q) = %q is not valid UTF-8", in, got)
		}
	}
}

func TestCompleteDirFollowsSymlinksAndKeepsTrailingSlash(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got := completeDir(root + "/li"); got != root+"/link/" {
		t.Errorf("a symlink to a directory should complete: %q", got)
	}
	if got := completeDir(root + "/link/"); got != root+"/link/inner/" {
		t.Errorf("tab after a slash: %q", got)
	}
	home, _ := os.UserHomeDir()
	if got := untilde("~/x/"); got != home+"/x/" {
		t.Errorf("untilde dropped the trailing slash: %q", got)
	}
}

func TestCompleteDirIgnoresCase(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Desktop", "Dev", "documents", "Área"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		root + "/desk": root + "/Desktop/",   // the one match, its case fixed
		root + "/de":   root + "/De",         // what Dev and Desktop share
		root + "/DO":   root + "/documents/", // upper case typed, lower case on disk
		root + "/á":    root + "/Área/",
		root + "/d":    root + "/d", // Dev, Desktop and documents share only the d
	}
	for in, want := range cases {
		if got := completeDir(in); got != want {
			t.Errorf("completeDir(%q) = %q, want %q", in, got, want)
		}
	}
	pr := &prompt{kind: promptNew, text: []rune(root + "/desk")}
	pr.refreshMatches()
	if g := pr.ghost(); g != "top/" {
		t.Errorf("ghost after desk: %q", g)
	}
}

func isValidUTF8(s string) bool { return strings.ToValidUTF8(s, "�") == s }

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "now"},
		{45 * time.Second, "45s"},
		{12 * time.Minute, "12m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{19*time.Hour + 9*time.Minute, "19h"},
		{72 * time.Hour, "3d"},
	} {
		if got := ago(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := ago(time.Time{}, now); got != "—" {
		t.Errorf("ago(zero) = %q", got)
	}
}
