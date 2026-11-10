package todo

import "github.com/LeikIsNRF/tt/internal/paths"

// Target is one todo file: a task's, or the inbox when Slug is "". Done
// targets a done task's archived file.
type Target struct {
	Slug string
	Done bool
	// Name and Ticket title a task file Ensure creates; Name defaults to
	// the slug.
	Name, Ticket string
}

var Inbox = Target{}

func (t Target) Path() string {
	switch {
	case t.Slug == "":
		return paths.Inbox()
	case t.Done:
		return DonePath(t.Slug)
	}
	return TaskPath(t.Slug)
}

func (t Target) Label() string {
	if t.Slug == "" {
		return "inbox"
	}
	return t.Slug
}

// Ensure creates the file if it's missing.
func (t Target) Ensure() error {
	switch {
	case t.Slug == "":
		return EnsureInbox()
	case t.Done:
		return ensure(t.Path(), taskTemplate(t.Slug, ""))
	}
	name := t.Name
	if name == "" {
		name = t.Slug
	}
	return EnsureTaskFile(t.Slug, name, t.Ticket)
}
