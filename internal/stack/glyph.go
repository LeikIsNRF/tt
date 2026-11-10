package stack

import "slices"

// Glyph is a branch's one-character state in the card's stack strip.
type Glyph string

const (
	Merged           Glyph = "✓"
	Approved         Glyph = "◆"
	InReview         Glyph = "●"
	ChangesRequested Glyph = "✎"
	CIRunning        Glyph = "⏳"
	CIFailing        Glyph = "✗"
	NoPR             Glyph = "○" // draft, closed, or never submitted
)

// GlyphFor picks the glyph for a branch's PR (nil: no PR). For an open PR
// the most actionable state wins: failing CI, then requested changes, then
// running CI, then the review state.
func GlyphFor(p *PR) Glyph {
	switch {
	case p == nil || p.State == "CLOSED" || (p.Draft && p.State == "OPEN"):
		return NoPR
	case p.State == "MERGED":
		return Merged
	case p.Checks == ChecksFailing:
		return CIFailing
	case p.Review == "CHANGES_REQUESTED":
		return ChangesRequested
	case p.Checks == ChecksPending:
		return CIRunning
	case p.Review == "APPROVED":
		return Approved
	}
	return InReview
}

// Describe spells out a PR's review and CI state for the detail pane.
func Describe(p *PR) string {
	if p == nil {
		return "not submitted"
	}
	switch p.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	review := map[string]string{
		"APPROVED": "approved", "CHANGES_REQUESTED": "changes requested",
	}[p.Review]
	if review == "" {
		review = "in review"
	}
	if p.Draft {
		review = "draft"
	}
	ci := map[Checks]string{
		ChecksPending: "CI running", ChecksFailing: "CI failing", ChecksPassing: "CI passed",
	}[p.Checks]
	if ci == "" {
		return review
	}
	return review + " · " + ci
}

// Keep returns the stack to show and remember: the freshly resolved one,
// with previously remembered branches that have since disappeared locally
// (`gt sync` deletes merged branches) put back underneath — but only those
// GitHub reports merged, so a branch you deleted or moved away drops out.
// Without PR data yet (known false), nothing remembered is dropped.
func Keep(remembered, resolved []string, prs map[string]PR, known bool) []string {
	var gone []string
	for _, b := range remembered {
		if slices.Contains(resolved, b) {
			continue
		}
		if p, ok := prs[b]; !known || (ok && p.State == "MERGED") {
			gone = append(gone, b)
		}
	}
	return append(gone, resolved...)
}

// Landed reports whether every branch in a non-empty stack is merged.
func Landed(branches []string, prs map[string]PR) bool {
	for _, b := range branches {
		if p, ok := prs[b]; !ok || p.State != "MERGED" {
			return false
		}
	}
	return len(branches) > 0
}
