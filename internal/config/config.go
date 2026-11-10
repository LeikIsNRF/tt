// Package config reads ~/.tt/config.toml. Every setting has a default, so a
// missing file is the same as an empty one.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/LeikIsNRF/tt/internal/paths"
)

// Config is tt's settings.
//
//	[task]
//	cwd = "~/src/app"  # pre-fills a new task's cwd
//	ticket_url = "https://example.atlassian.net/browse/%s"
//
//	[draft]
//	model = "haiku"    # any `claude --model` value
//	timeout = "30s"
type Config struct {
	Task  Task  `toml:"task"`
	Draft Draft `toml:"draft"`
}

// Task configures new tasks and their tickets.
type Task struct {
	Cwd string `toml:"cwd"`
	// TicketURL turns a ticket ID into a link: a format with one %s. Empty
	// means tickets that aren't URLs can't be opened.
	TicketURL string `toml:"ticket_url"`
}

// Draft configures the create form's ctrl+g draft call.
type Draft struct {
	Model   string   `toml:"model"`
	Timeout Duration `toml:"timeout"`
}

// Duration is a time.Duration written as "30s" in TOML.
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	if v <= 0 {
		return fmt.Errorf("duration must be positive, got %s", v)
	}
	*d = Duration(v)
	return nil
}

func defaults() Config {
	return Config{
		Task:  Task{Cwd: "~"},
		Draft: Draft{Model: "haiku", Timeout: Duration(30 * time.Second)},
	}
}

// Load reads the config file over the defaults.
func Load() (Config, error) { return load(paths.Config()) }

func load(path string) (Config, error) {
	c := defaults()
	if _, err := toml.DecodeFile(path, &c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return defaults(), fmt.Errorf("%s: %w", path, err)
	}
	if c.Draft.Model == "" {
		c.Draft.Model = defaults().Draft.Model
	}
	if c.Task.Cwd == "" {
		c.Task.Cwd = defaults().Task.Cwd
	}
	if u := c.Task.TicketURL; u != "" && strings.Count(u, "%s") != 1 {
		return defaults(), fmt.Errorf("%s: [task] ticket_url needs exactly one %%s, got %q", path, u)
	}
	return c, nil
}
