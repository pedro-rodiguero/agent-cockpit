package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		return home + p[1:] // not filepath.Join, which would drop a trailing "/"
	}
	return p
}

// dirMatches lists the directories the last element of p completes to, as paths ending in "/", in the
// form tilde gives. Case is ignored, as oh-my-zsh does by default, and symlinks to directories count.
// Hidden directories are offered only when the typed name starts with a dot.
func dirMatches(p string) []string {
	dir, base := filepath.Split(untilde(p))
	if dir == "" {
		dir = "."
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var matches []string
	for _, e := range ents {
		name := e.Name()
		if !hasPrefixFold(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		full := filepath.Join(dir, name)
		if e.IsDir() || (e.Type()&os.ModeSymlink != 0 && isDir(full)) {
			matches = append(matches, tilde(full)+"/")
		}
	}
	return matches
}

// listProblem says why the folder p completes in cannot be listed, when the system refuses it: macOS
// keeps the Desktop, Documents and Downloads from an app until the person allows it, and completion
// would otherwise just find nothing. A folder that does not exist (yet) is no problem.
func listProblem(p string) string {
	dir, _ := filepath.Split(untilde(p))
	if dir == "" {
		dir = "."
	}
	if _, err := os.ReadDir(dir); err != nil && (os.IsPermission(err) || errors.Is(err, syscall.EPERM)) {
		return "cannot read " + tilde(dir) + ": allow Agent Cockpit in System Settings › Privacy & Security › Files and Folders"
	}
	return ""
}

// completeDir extends a path to the longest common prefix of the directories it matches, adding a
// trailing "/" when exactly one matches.
func completeDir(p string) string { return extendPath(p, dirMatches(p)) }

// extendPath is completeDir for the matches of p already listed.
func extendPath(p string, matches []string) string {
	switch len(matches) {
	case 0:
		return p
	case 1:
		return matches[0]
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	dir, base := filepath.Split(untilde(p))
	common := commonPrefix(names)
	if n, b := len([]rune(common)), len([]rune(base)); n < b || (n == b && common == base) {
		return p
	}
	if dir == "" {
		return common
	}
	return tilde(filepath.Join(dir, common))
}

// hasPrefixFold is strings.HasPrefix ignoring case.
func hasPrefixFold(s, prefix string) bool {
	r, p := []rune(s), []rune(prefix)
	return len(r) >= len(p) && strings.EqualFold(string(r[:len(p)]), prefix)
}

// matchName is the last element of a match, with its slash: what a list of matches shows.
func matchName(m string) string { return filepath.Base(m) + "/" }

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
