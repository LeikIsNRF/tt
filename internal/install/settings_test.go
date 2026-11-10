package install

import (
	"encoding/json"
	"strings"
	"testing"
)

const cmd = "/usr/local/bin/tt hook"

var events = []string{"SessionStart", "Stop"}

// Mirrors the shape of a real settings.json: unrelated keys, a foreign hook
// sharing an event with tt, and an "&&" that must not be HTML-escaped.
const existing = `{
  "model": "opus",
  "statusLine": {
    "type": "command",
    "command": "npx -y ccstatusline@latest && true"
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "say done"
          }
        ]
      }
    ]
  },
  "autoMode": {
    "allow": [
      "<x>"
    ]
  }
}
`

func TestInstallKeepsEverythingElse(t *testing.T) {
	out, err := rewrite([]byte(existing), events, cmd)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"say done"`, `&& true`, `"<x>"`} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %s:\n%s", want, s)
		}
	}
	if strings.Index(s, `"model"`) > strings.Index(s, `"autoMode"`) {
		t.Errorf("key order changed:\n%s", s)
	}
	found, _ := registeredEvents(out)
	if len(found) != 2 || found["Stop"] != cmd {
		t.Errorf("registered = %v", found)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	once, _ := rewrite([]byte(existing), events, cmd)
	twice, _ := rewrite(once, events, cmd)
	if string(once) != string(twice) {
		t.Errorf("second install changed the file:\n%s\n---\n%s", once, twice)
	}
}

func TestReinstallReplacesStaleCommand(t *testing.T) {
	old, _ := rewrite([]byte(existing), events, "/old/path/tt hook")
	out, _ := rewrite(old, events, cmd)
	if strings.Contains(string(out), "/old/path") {
		t.Errorf("stale command kept:\n%s", out)
	}
}

func TestUninstallRestoresOriginal(t *testing.T) {
	for name, orig := range map[string]string{
		"with other hooks": existing,
		"no hooks key":     "{\n  \"model\": \"opus\"\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			installed, _ := rewrite([]byte(orig), events, cmd)
			out, err := rewrite(installed, events, "")
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != orig {
				t.Errorf("got:\n%s\nwant:\n%s", out, orig)
			}
		})
	}
}

func TestUninstallKeepsForeignHookInSharedGroup(t *testing.T) {
	in := `{"hooks":{"Stop":[{"matcher":"","hooks":[
		{"type":"command","command":"say done"},
		{"type":"command","command":"'/a b/tt' hook"}]}]}}`
	out, err := rewrite([]byte(in), events, "")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher *string
			Hooks   []struct{ Command string }
		}
	}
	if err := json.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	g := s.Hooks["Stop"]
	if len(g) != 1 || g[0].Matcher == nil || len(g[0].Hooks) != 1 || g[0].Hooks[0].Command != "say done" {
		t.Errorf("got %s", out)
	}
}

func TestEmptyFile(t *testing.T) {
	out, err := rewrite(nil, events, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := registeredEvents(out); len(found) != 2 {
		t.Errorf("got %s", out)
	}
}
