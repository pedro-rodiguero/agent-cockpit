package mux

// foregroundGroup is 0 on Windows, which has no foreground process group to ask a terminal for: a pane
// there reports the program it was started with.
func (p *pane) foregroundGroup() int { return 0 }
