package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, err := load(filepath.Join(dir, "missing.toml"))
	if err != nil || c.Draft.Model != "haiku" || time.Duration(c.Draft.Timeout) != 30*time.Second {
		t.Fatalf("missing file: got %+v, %v", c, err)
	}

	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[draft]\nmodel = \"sonnet\"\ntimeout = \"45s\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = load(p)
	if err != nil || c.Draft.Model != "sonnet" || time.Duration(c.Draft.Timeout) != 45*time.Second {
		t.Fatalf("got %+v, %v", c, err)
	}

	if err := os.WriteFile(p, []byte("[draft]\ntimeout = \"soon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := load(p); err == nil {
		t.Error("bad timeout: no error")
	}

	if err := os.WriteFile(p, []byte("[task]\nticket_url = \"https://x/browse\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := load(p); err == nil {
		t.Error("ticket_url without a format verb: no error")
	}
}
