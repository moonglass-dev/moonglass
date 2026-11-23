package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Ticket is one ticket line of docs/plan/build-plan.md §3.
type Ticket struct {
	ID     string
	Title  string
	Risk   string
	Kind   string
	Line   string
	Deps   []string
	Ticked bool
}

var (
	lineRe    = regexp.MustCompile(`^- \[([ x])\] \*\*([A-Za-z0-9-]+)\*\* (.+)$`)
	subjectRe = regexp.MustCompile(`^([A-Za-z0-9-]+): .+ \(#[0-9]+\)$`)
)

// parsePlan reads every ticket line. A malformed ticket line is an error, so
// a typo never hides a ticket.
func parsePlan(text string) ([]Ticket, error) {
	var out []Ticket
	seen := map[string]bool{}
	for n, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "- [") || !strings.Contains(line, "**") {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("build-plan.md:%d: want `- [ ] **ID** Title — intent · depends_on: … · risk: …`, got %q", n+1, line)
		}
		t := Ticket{ID: m[2], Ticked: m[1] == "x", Line: line, Kind: "build"}
		rest := m[3]
		head, _, _ := strings.Cut(rest, " · ")
		title, _, ok := strings.Cut(head, " — ")
		if !ok {
			return nil, fmt.Errorf("build-plan.md:%d: %s has no ` — ` after its title", n+1, t.ID)
		}
		t.Title = title
		for _, field := range strings.Split(rest, " · ")[1:] {
			key, value, _ := strings.Cut(field, ": ")
			switch key {
			case "depends_on":
				if value != "—" {
					for _, d := range strings.Split(value, ",") {
						t.Deps = append(t.Deps, strings.TrimSpace(d))
					}
				}
			case "risk":
				t.Risk = value
			case "kind":
				t.Kind = value
			}
		}
		if t.Risk != "low" && t.Risk != "standard" && t.Risk != "high" {
			return nil, fmt.Errorf("build-plan.md:%d: %s risk %q, want low, standard or high", n+1, t.ID, t.Risk)
		}
		if t.Kind != "build" && t.Kind != "human" {
			return nil, fmt.Errorf("build-plan.md:%d: %s kind %q, want build or human", n+1, t.ID, t.Kind)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("build-plan.md:%d: %s appears twice", n+1, t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	for _, t := range out {
		for _, d := range t.Deps {
			if !seen[d] {
				return nil, fmt.Errorf("%s depends on %s, which is not in the plan", t.ID, d)
			}
		}
	}
	return out, nil
}

// mergedIDs returns the ID of every squash-merge subject on main,
// "<ID>: <title> (#<PR>)".
func mergedIDs(subjects []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range subjects {
		if m := subjectRe.FindStringSubmatch(s); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

// PR is an open pull request from a p/<ID> branch.
type PR struct {
	ID     string
	Checks string // pass, fail, pending or none
	Number int
	Auto   bool
	Draft  bool
	High   bool
	Owner  bool // labelled needs-owner: parked, so it holds no slot
}

// State is everything the dispatcher needs to decide its next step.
type State struct {
	Done        map[string]bool
	Owner       map[string]int // ID -> pull request waiting for the owner
	Ready       []Ticket
	Start       []Ticket
	Human       []Ticket
	PRs         []PR
	Total       int
	HighRunning bool
}

// At most MaxRunning tickets are in flight, and at most one of them is high
// risk. DriftMerges merges or DriftDays days, whichever comes first, make the
// drift check due.
const (
	MaxRunning  = 3
	DriftMerges = 15
	DriftDays   = 7
)

func status(plan []Ticket, merged map[string]bool, prs []PR) State {
	st := State{Done: map[string]bool{}, Owner: map[string]int{}, PRs: prs, Total: len(plan)}
	byID := map[string]Ticket{}
	for _, t := range plan {
		byID[t.ID] = t
		if t.Ticked || merged[t.ID] {
			st.Done[t.ID] = true
		}
	}
	busy := map[string]bool{}
	for _, p := range prs {
		if p.Owner {
			st.Owner[p.ID] = p.Number
			continue
		}
		busy[p.ID] = true
		if p.High || byID[p.ID].Risk == "high" {
			st.HighRunning = true
		}
	}
	for _, t := range plan {
		if st.Done[t.ID] || busy[t.ID] || st.Owner[t.ID] != 0 {
			continue
		}
		if !slices.ContainsFunc(t.Deps, func(d string) bool { return !st.Done[d] }) {
			if t.Kind == "human" {
				st.Human = append(st.Human, t)
			} else {
				st.Ready = append(st.Ready, t)
			}
		}
	}
	slots := MaxRunning - len(busy)
	high := st.HighRunning
	for _, t := range st.Ready {
		if slots <= 0 {
			break
		}
		if t.Risk == "high" {
			if high {
				continue
			}
			high = true
		}
		st.Start = append(st.Start, t)
		slots--
	}
	return st
}

// tick marks the box of every merged ticket and returns the new plan text and
// the IDs it ticked.
func tick(text string, merged map[string]bool) (string, []string, error) {
	if _, err := parsePlan(text); err != nil {
		return "", nil, err
	}
	lines := strings.Split(text, "\n")
	var ticked []string
	for i, l := range lines {
		if m := lineRe.FindStringSubmatch(l); m != nil && m[1] == " " && merged[m[2]] {
			lines[i] = "- [x]" + strings.TrimPrefix(l, "- [ ]")
			ticked = append(ticked, m[2])
		}
	}
	return strings.Join(lines, "\n"), ticked, nil
}
