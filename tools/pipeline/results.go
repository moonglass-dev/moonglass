package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Result files live in docs/tickets/<ID>/ on the ticket branch and merge with it.
const (
	roundsFile   = "rounds.json"
	planFile     = "plan.md"
	approvalFile = "approval.json"
	lockFile     = "tests.lock"
	checkFile    = "check.json"
	reviewFile   = "review.json"
	mergeFile    = "merge.json"
	blockedFile  = "BLOCKED.md"
)

// limits caps how many times each step may be claimed for one ticket.
var limits = map[string]int{
	"worker":  3,
	"ticket":  2,
	"plan":    2,
	"tests":   2,
	"approve": 2,
	"build":   2,
	"review":  3,
	"fix":     1,
	"repair":  1,
	"arch":    2,
}

// rounds counts claims per step and remembers the open claim.
type rounds struct {
	Counts map[string]int `json:"counts"`
	Claim  *claim         `json:"claim"`
}

type claim struct {
	Step  string `json:"step"`
	Round int    `json:"round"`
}

type finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Scenario string `json:"scenario"`
}

type approval struct {
	Round    int      `json:"round"`
	Verdict  string   `json:"verdict"`
	Blockers []string `json:"blockers"`
	Notes    string   `json:"notes"`
}

type review struct {
	Round    int       `json:"round"`
	Verdict  string    `json:"verdict"`
	Skipped  bool      `json:"skipped"`
	Reason   string    `json:"reason"`
	Commit   string    `json:"commit"`
	Blockers []finding `json:"blockers"`
	Majors   []finding `json:"majors"`
	Minors   []finding `json:"minors"`
}

type gateResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type checkResult struct {
	Commit             string       `json:"commit"`
	OK                 bool         `json:"ok"`
	Gates              []gateResult `json:"gates"`
	DiffLines          int          `json:"diff_lines"`
	ChangedCoverage    *float64     `json:"changed_line_coverage"`
	SkipReviewEligible bool         `json:"skip_review_eligible"`
	SkipReviewReason   string       `json:"skip_review_reason"`
}

type mergeResult struct {
	Result  string `json:"result"`
	Commit  string `json:"commit"`
	Detail  string `json:"detail"`
	Handled bool   `json:"handled"`
}

func decodeStrict(path string, b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readJSON(t tree, path string, v any) (bool, error) {
	b, ok, err := t.read(path)
	if err != nil || !ok {
		return ok, err
	}
	return true, decodeStrict(path, b, v)
}

func encodeJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("encode %T: %v", v, err))
	}
	return append(b, '\n')
}

func loadRounds(t tree, id string) (rounds, error) {
	r := rounds{Counts: map[string]int{}}
	if _, err := readJSON(t, ticketDir(id)+roundsFile, &r); err != nil {
		return r, err
	}
	if r.Counts == nil {
		r.Counts = map[string]int{}
	}
	return r, nil
}

func (a approval) validate(round int) error {
	switch {
	case a.Round != round:
		return fmt.Errorf("approval.json round is %d, the claimed round is %d", a.Round, round)
	case a.Verdict == "approve" && len(a.Blockers) > 0:
		return fmt.Errorf("approval.json approves but lists blockers")
	case a.Verdict == "revise" && len(a.Blockers) == 0:
		return fmt.Errorf("approval.json asks for a revision but lists no blockers")
	case a.Verdict != "approve" && a.Verdict != "revise":
		return fmt.Errorf("approval.json verdict must be approve or revise, got %q", a.Verdict)
	}
	return nil
}

func (r review) validate(round int) error {
	switch {
	case r.Round != round:
		return fmt.Errorf("review.json round is %d, the claimed round is %d", r.Round, round)
	case r.Verdict != "approve" && r.Verdict != "changes":
		return fmt.Errorf("review.json verdict must be approve or changes, got %q", r.Verdict)
	case r.Verdict == "approve" && len(r.Blockers)+len(r.Majors) > 0:
		return fmt.Errorf("review.json approves but lists blockers or majors")
	case r.Verdict == "changes" && len(r.Blockers)+len(r.Majors) == 0:
		return fmt.Errorf("review.json asks for changes but lists no blockers or majors")
	case r.Skipped:
		return fmt.Errorf("review.json from a reviewer must not set skipped")
	}
	for _, f := range slices.Concat(r.Blockers, r.Majors, r.Minors) {
		if f.File == "" || f.Issue == "" {
			return fmt.Errorf("review.json: every finding needs file and issue")
		}
	}
	return nil
}

// planVerdict reads the front matter of plan.md.
func planVerdict(text string) (verdict, reason string, err error) {
	front, body, err := splitFrontMatter(text)
	if err != nil {
		return "", "", fmt.Errorf("plan.md: %w", err)
	}
	fields, err := parseFields(front)
	if err != nil {
		return "", "", fmt.Errorf("plan.md: %w", err)
	}
	verdict, reason = fields["verdict"].scalar, fields["reason"].scalar
	switch verdict {
	case "ready":
		for _, s := range []string{"## Files to change", "## Tests to write", "## Edge cases"} {
			if !strings.Contains(body, s) {
				return "", "", fmt.Errorf("plan.md: missing section %s", s)
			}
		}
	case "needs-human":
		if reason == "" {
			return "", "", fmt.Errorf("plan.md: verdict needs-human needs a reason")
		}
	default:
		return "", "", fmt.Errorf("plan.md: verdict must be ready or needs-human, got %q", verdict)
	}
	return verdict, reason, nil
}
