package tui

import "strings"

// helpKeys is the full key list `?` shows, in groups. It mirrors navBindings.
var helpKeys = [][]keyHint{
	{
		{"←↑↓→ hjkl", "move; right from the last column enters the rail"},
		{"tab", "next card · shift-tab previous"},
		{"1–9", "card on this page"},
		{"[ ]", "previous and next page · g G first and last"},
		{"click", "select a card; a running pane takes the keyboard · drag selects text, copied on release"},
		{"", "double click copies a word or URL · triple click a line · a click on the rail leaves a pane"},
		{"HJKL", "move the selected card · also shift and an arrow, or drag its title"},
		{"> <", "bigger or smaller · = all the same size · or drag a border"},
		{"wheel", "scroll a pane back through its history, or a full-screen program like Claude Code itself · typing returns to the live screen"},
	},
	{
		{"⏎ i", "the obvious thing: type into a pane, continue a tab's session here,"},
		{"", "reopen an ended pane, go to a tab, open an inbox item"},
		{"o", "go to the session's terminal tab"},
		{"p", "move a session from its terminal tab into a pane"},
		{"r", "send a message to a Claude Code session · alt-⏎ or \\⏎ starts a new line"},
		{"m", "read the session's plan, live as it changes, even before plan mode writes it · r sends feedback"},
		{"n", "new pane"},
		{"x", "close a pane, stopping its agent; delete a task"},
		{"X", "clean up for a new day: close every pane, or only the ones at rest"},
		{"s", "set a pane aside in the rail, or bring it back · quiet panes go there on their own"},
		{"", "a quiet ssh pane is offered the rail (y · n · a always); ⏎ brings it back"},
		{"ctrl-q", "leave a pane, and its zoom; every other key goes to the agent"},
		{"ctrl-z", "zoom in or out of the pane you are typing into"},
	},
	{
		{"+", "new task: \"call the venue @tomorrow #personal\""},
		{"space", "complete the selected task · u reopens it"},
		{"e", "edit the selected task"},
	},
	{
		{"z", "zoom the selected card · z or esc leaves; typing goes to a zoomed pane"},
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
