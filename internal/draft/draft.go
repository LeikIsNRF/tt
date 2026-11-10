// Package draft turns a rough description into a task draft (name, ticket,
// todos, notes) with one plain `claude -p` call: no tools, no MCP servers,
// no settings, so it can't read code or fetch tickets, and tt's own hook
// doesn't see it as a session.
package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Draft is what the create form previews and, on save, writes.
type Draft struct {
	Name   string   `json:"name"`
	Ticket string   `json:"ticket"`
	Todos  []string `json:"todos"`
	Notes  string   `json:"notes"`
}

const systemPrompt = `You turn a developer's rough description of a piece of work into a task draft for their personal task tracker.

Reply with JSON only: {"name", "ticket", "todos", "notes"}.
- name: a short task title, 2-6 words, sentence case, no ticket ID, no trailing period.
- ticket: an issue key like PROJ-42 or ABC-12, only if one appears in the description, copied exactly. Otherwise "". Never invent one. A key is only an ID: don't guess what its letters stand for.
- todos: 2-6 concrete next steps, each one short line in the imperative. Only steps the description implies; don't pad.
- notes: one or two sentences of context from the description that the name and todos don't already carry, or "" if there is none. Don't comment on the description itself or on how the draft was made.

You can't see their code or their issue tracker. Use only what the description says.`

var schema = `{"type":"object","additionalProperties":false,"required":["name","ticket","todos","notes"],"properties":{` +
	`"name":{"type":"string"},"ticket":{"type":"string"},"todos":{"type":"array","items":{"type":"string"}},"notes":{"type":"string"}}}`

// ErrTimeout means ctx's deadline passed before claude answered.
var ErrTimeout = errors.New("timed out")

// Run drafts a task from desc with model, using the claude binary at bin.
func Run(ctx context.Context, bin, model, desc string) (Draft, error) {
	cmd := exec.CommandContext(ctx, bin, "-p",
		"--output-format", "json",
		"--model", model,
		"--system-prompt", systemPrompt,
		"--json-schema", schema,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--no-session-persistence",
	)
	// Through stdin, so a description starting with "-" can't be read as a flag.
	cmd.Stdin = strings.NewReader(desc)
	// Outside any project, so no project settings or CLAUDE.md apply.
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second // don't hang on pipes a killed claude left open
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return Draft{}, ErrTimeout
	}
	if ctx.Err() != nil {
		return Draft{}, ctx.Err()
	}
	d, perr := Parse(stdout.Bytes())
	if err == nil {
		return d, perr
	}
	// A JSON error reply says more than the exit status; else stderr.
	var re *replyError
	if errors.As(perr, &re) {
		return Draft{}, perr
	}
	if msg := oneLine(stderr.String()); msg != "" {
		return Draft{}, fmt.Errorf("claude: %s", msg)
	}
	return Draft{}, fmt.Errorf("claude: %w", err)
}

type replyError struct{ msg string }

func (e *replyError) Error() string { return "claude: " + e.msg }

// Parse reads `claude -p --output-format json` output: the schema-checked
// structured_output if it's there, else the result text as JSON.
func Parse(out []byte) (Draft, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return Draft{}, errors.New("claude's reply isn't JSON")
	}
	if r.IsError {
		msg := oneLine(r.Result)
		if msg == "" {
			msg = "error with no message"
		}
		return Draft{}, &replyError{msg}
	}
	body := []byte(r.StructuredOutput)
	if len(body) == 0 || string(body) == "null" {
		body = []byte(unfence(r.Result))
	}
	var d Draft
	if err := json.Unmarshal(body, &d); err != nil {
		return Draft{}, errors.New("the draft isn't the JSON asked for")
	}
	return d.clean()
}

func (d Draft) clean() (Draft, error) {
	d.Name = oneLine(d.Name)
	d.Ticket = strings.TrimSpace(d.Ticket)
	d.Notes = strings.TrimSpace(d.Notes)
	var todos []string
	for _, t := range d.Todos {
		if t = oneLine(t); t != "" {
			todos = append(todos, t)
		}
	}
	d.Todos = todos
	if d.Name == "" {
		return Draft{}, errors.New("the draft has no name")
	}
	return d, nil
}

// unfence strips a ```json fence a model may wrap its JSON in.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
