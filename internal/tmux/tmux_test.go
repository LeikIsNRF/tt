package tmux

import "testing"

func TestClaudePanesWalksProcessTree(t *testing.T) {
	ps := `    1     0 /sbin/launchd
  100     1 -zsh
  101   100 claude --model opus
  200     1 -zsh
  201   200 vim notes.md
  300     1 -zsh
  301   300 node /opt/lib/node_modules/@anthropic-ai/claude-code/cli.js
`
	panes := []Pane{{ID: "%1", PID: 100}, {ID: "%2", PID: 200}, {ID: "%3", PID: 300}}
	got := claudePanes(panes, ps)
	if !got["%1"] || got["%2"] || !got["%3"] {
		t.Errorf("got %v", got)
	}
}
