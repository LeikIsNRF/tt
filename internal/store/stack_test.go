package store

import (
	"slices"
	"testing"
	"time"

	"github.com/LeikIsNRF/tt/internal/stack"
)

func TestSetBranchForgetsRememberedStack(t *testing.T) {
	s := open(t)
	id, _ := s.CreateTask("t", "T", "/x", "")
	must(t, s.SaveStack(id, nil, []string{"a", "b"}))
	must(t, s.SetLandedOffered(id, true))
	must(t, s.SetBranch(id, "z"))
	stacks, _ := s.Stacks()
	task, _, _ := s.TaskBySlug("t")
	if len(stacks[id]) != 0 || task.Branch != "z" || task.LandedOffered {
		t.Errorf("stack %v, task %+v", stacks[id], task)
	}
}

func TestPRCacheRoundTrip(t *testing.T) {
	s := open(t)
	if _, at, _ := s.PRs(); !at.IsZero() {
		t.Fatal("fresh DB has a fetch time")
	}
	in := stack.PRs{"o/r": {"a": {Number: 7, URL: "u", State: "OPEN", Draft: true,
		Review: "APPROVED", Checks: stack.ChecksFailing}}}
	now := time.Unix(1_800_000_000, 0)
	must(t, s.SavePRs(in, now))
	must(t, s.SavePRs(in, now)) // replaces, doesn't duplicate
	got, at, err := s.PRs()
	if err != nil || !at.Equal(now) || len(got["o/r"]) != 1 || got["o/r"]["a"] != in["o/r"]["a"] {
		t.Errorf("got %v at %v, %v", got, at, err)
	}
	id, _ := s.CreateTask("t", "T", "/x", "")
	must(t, s.AddLabel(id, "x"))
	must(t, s.AddLabel(id, "x"))
	if ls, _ := s.Labels(); !slices.Equal(ls[id], []string{"x"}) {
		t.Errorf("labels %v", ls)
	}
}

func TestDeleteTaskRemovesWhatPointsAtIt(t *testing.T) {
	s := open(t)
	id, _ := s.CreateTask("t", "T", "/x", "")
	keep, _ := s.CreateTask("k", "K", "/x", "")
	for _, task := range []int64{id, keep} {
		must(t, s.AddLabel(task, "x"))
		must(t, s.SaveStack(task, nil, []string{"a"}))
	}
	must(t, s.DeleteTask(id))
	if _, ok, _ := s.TaskBySlug("t"); ok {
		t.Error("task still there")
	}
	ls, _ := s.Labels()
	stacks, _ := s.Stacks()
	if len(ls[id]) != 0 || len(stacks[id]) != 0 || len(ls[keep]) != 1 || len(stacks[keep]) != 1 {
		t.Errorf("labels %v, stacks %v", ls, stacks)
	}
	if taken, _ := s.SlugTaken("t"); taken {
		t.Error("slug still taken")
	}
}
