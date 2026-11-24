package tui

import "strings"

// helpKeys is the full key list `?` shows, in groups. It mirrors navBindings.
var helpKeys = [][]keyHint{
	{
		{"←↑↓→ hjkl", "move; right from the last column enters the rail"},
		{"tab", "next card · shift-tab previous"},
		{"1–9", "card on this page"},
		{"[ ]", "previous and next page · g G first and last"},
	},
	{
		{"⏎", "the obvious thing: type into a pane, continue a tab's session here,"},
		{"", "reopen an ended pane, go to a tab, open an inbox item"},
		{"o", "go to the session's terminal tab"},
		{"r", "send a message to a Claude Code session"},
		{"n", "new pane"},
		{"x", "close a pane, stopping its agent"},
		{"ctrl-q", "leave a pane; every other key goes to the agent"},
	},
	{
		{"z", "zoom the selected card · esc leaves"},
		{"!", "next card that needs you · a shows only those"},
		{"v", "grid or list"},
		{"t", "tab titles on or off"},
		{"q", "quit; panes keep running"},
	},
}

// helpBody is the key list, centered in a w×h body.
func helpBody(w, h int) []string {
	const keyCol = 12
	var rows []string
	for i, group := range helpKeys {
		if i > 0 {
			rows = append(rows, "")
		}
		for _, k := range group {
			rows = append(rows, paint(cGold, cBg, fit(k.key, keyCol))+paint(cSoft, cBg, k.does))
		}
	}
	boxW := 0
	for _, r := range rows {
		boxW = max(boxW, width(r))
	}
	left := strings.Repeat(" ", max(0, (w-boxW)/2))
	out := make([]string, 0, h)
	for y := 0; y < max(0, (h-len(rows)-2)/2); y++ { // two rows for the heading
		out = append(out, pad("", w, cBg))
	}
	out = append(out, pad(paintBold(cGoldHi, cBg, left+"KEYS"), w, cBg), pad("", w, cBg))
	for _, r := range rows {
		out = append(out, pad(paint(cInk, cBg, left)+r, w, cBg))
	}
	return out
}
