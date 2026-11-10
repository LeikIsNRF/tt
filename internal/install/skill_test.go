package install

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeikIsNRF/tt/internal/paths"
)

func TestSkillInstallAndRemove(t *testing.T) {
	t.Setenv("TT_CLAUDE_SETTINGS", filepath.Join(t.TempDir(), "settings.json"))
	if err := writeSkill(io.Discard, "/bin/tt"); err != nil {
		t.Fatal(err)
	}
	if !skillCurrent("/bin/tt") || skillCurrent("/other/tt") {
		t.Error("skillCurrent doesn't track the installed binary")
	}
	if err := removeSkill(io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(paths.ClaudeSkill())); !os.IsNotExist(err) {
		t.Errorf("skill dir left behind: %v", err)
	}
}

func TestSkillLeavesForeignOneAlone(t *testing.T) {
	t.Setenv("TT_CLAUDE_SETTINGS", filepath.Join(t.TempDir(), "settings.json"))
	path := paths.ClaudeSkill()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSkill(io.Discard, "/bin/tt"); err == nil {
		t.Error("overwrote a skill tt didn't write")
	}
	if err := removeSkill(io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine" {
		t.Error("removed a skill tt didn't write")
	}
}
