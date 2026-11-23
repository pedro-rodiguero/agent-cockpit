package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// tilde shortens a path under the home directory to "~/...".
func tilde(p string) string {
	home, _ := os.UserHomeDir()
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// untilde expands a leading "~" to the home directory.
func untilde(p string) string {
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// completeDir extends a path to the longest common prefix of the directories it matches, adding a
// trailing "/" when exactly one matches. Hidden directories are offered only when the typed name starts
// with a dot.
func completeDir(p string) string {
	dir, base := filepath.Split(untilde(p))
	if dir == "" {
		dir = "."
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return p
	}
	var matches []string
	for _, e := range ents {
		name := e.Name()
		hidden := strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")
		if e.IsDir() && strings.HasPrefix(name, base) && !hidden {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return p
	}
	if len(matches) == 1 {
		return tilde(filepath.Join(dir, matches[0]) + "/")
	}
	common := commonPrefix(matches)
	if common == base {
		return p
	}
	return tilde(filepath.Join(dir, common))
}

// commonPrefix is the longest prefix, in whole characters, that every string in s starts with.
func commonPrefix(s []string) string {
	common := []rune(s[0])
	for _, x := range s[1:] {
		r := []rune(x)
		n := 0
		for n < len(common) && n < len(r) && common[n] == r[n] {
			n++
		}
		common = common[:n]
	}
	return string(common)
}
