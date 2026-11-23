package main

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixture = `# Plan

Some prose that mentions **W9-9** but is not a ticket line.

- [x] **A-1** First — does a thing · depends_on: — · risk: low
- [ ] **A-2** Second — needs A-1 · depends_on: A-1 · risk: high · paths: internal/x/
- [ ] **A-3** Third — needs A-2 · depends_on: A-2 · risk: standard
- [ ] **A-4** Fourth — another high one · depends_on: A-1 · risk: high
- [ ] **A-5** Fifth — owner work · depends_on: — · risk: low · kind: human
- [ ] **A-6** Sixth — waits on the owner ticket · depends_on: A-5 · risk: low
- [ ] **A-7** Seventh — standalone · depends_on: — · risk: low
- [ ] **A-8** Eighth — standalone · depends_on: — · risk: standard
`

func ids(ts []Ticket) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

func TestParsePlanReadsTicketLines(t *testing.T) {
	plan, err := parsePlan(fixture)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}
	if got, want := ids(plan), []string{"A-1", "A-2", "A-3", "A-4", "A-5", "A-6", "A-7", "A-8"}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	a2 := plan[1]
	if a2.Title != "Second" || a2.Risk != "high" || a2.Kind != "build" || a2.Ticked || !slices.Equal(a2.Deps, []string{"A-1"}) {
		t.Errorf("A-2 = %+v, want title Second, risk high, kind build, unticked, deps [A-1]", a2)
	}
	if !plan[0].Ticked || plan[0].Deps != nil {
		t.Errorf("A-1 ticked=%t deps=%v, want ticked and no deps", plan[0].Ticked, plan[0].Deps)
	}
	if plan[4].Kind != "human" {
		t.Errorf("A-5 kind = %q, want human", plan[4].Kind)
	}
}

func TestParsePlanRejectsBadLines(t *testing.T) {
	tests := []struct {
		name, line, want string
	}{
		{"no checkbox state", "- [?] **B-1** T — i · depends_on: — · risk: low", "want `- [ ] **ID**"},
		{"no intent", "- [ ] **B-1** Title only · depends_on: — · risk: low", "no ` — `"},
		{"bad risk", "- [ ] **B-1** T — i · depends_on: — · risk: medium", `risk "medium"`},
		{"bad kind", "- [ ] **B-1** T — i · depends_on: — · risk: low · kind: review", `kind "review"`},
		{"unknown dependency", "- [ ] **B-1** T — i · depends_on: Z-9 · risk: low", "depends on Z-9"},
		{"duplicate", "- [ ] **B-1** T — i · depends_on: — · risk: low\n- [ ] **B-1** T — i · depends_on: — · risk: low", "appears twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePlan(tt.line)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parsePlan error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestMergedIDsFromSquashSubjects(t *testing.T) {
	got := mergedIDs([]string{"A-2: Second (#4)", "A-3: status built", "Merge branch 'main'", "Q-cli-timeout: CLI timeout (#9)"})
	want := map[string]bool{"A-2": true, "Q-cli-timeout": true}
	if len(got) != len(want) || !got["A-2"] || !got["Q-cli-timeout"] {
		t.Errorf("mergedIDs = %v, want %v: only squash subjects ending in (#N) count", got, want)
	}
}

func TestStatus(t *testing.T) {
	plan, err := parsePlan(fixture)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}
	tests := []struct {
		name      string
		merged    map[string]bool
		prs       []PR
		wantReady []string
		wantStart []string
		wantHuman []string
	}{
		{
			name:      "one high at a time, three slots",
			wantReady: []string{"A-2", "A-4", "A-7", "A-8"},
			wantStart: []string{"A-2", "A-7", "A-8"},
			wantHuman: []string{"A-5"},
		},
		{
			name:      "a merged dependency unlocks its dependent",
			merged:    map[string]bool{"A-2": true},
			wantReady: []string{"A-3", "A-4", "A-7", "A-8"},
			wantStart: []string{"A-3", "A-4", "A-7"},
			wantHuman: []string{"A-5"},
		},
		{
			name:      "an open high-risk PR holds the other high tickets",
			prs:       []PR{{ID: "A-2", Number: 4}},
			wantReady: []string{"A-4", "A-7", "A-8"},
			wantStart: []string{"A-7", "A-8"},
			wantHuman: []string{"A-5"},
		},
		{
			name:      "a needs-owner PR is parked and holds no slot",
			prs:       []PR{{ID: "A-7", Number: 3, Owner: true}, {ID: "A-4", Number: 5, Owner: true}, {ID: "A-8", Number: 6}},
			wantReady: []string{"A-2"},
			wantStart: []string{"A-2"},
			wantHuman: []string{"A-5"},
		},
		{
			name:      "a request PR uses a slot",
			prs:       []PR{{ID: "Q-x", Number: 7, High: true}, {ID: "Q-y", Number: 8}},
			wantReady: []string{"A-2", "A-4", "A-7", "A-8"},
			wantStart: []string{"A-7"},
			wantHuman: []string{"A-5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := status(plan, tt.merged, tt.prs)
			if got := ids(st.Ready); !slices.Equal(got, tt.wantReady) {
				t.Errorf("ready = %v, want %v", got, tt.wantReady)
			}
			if got := ids(st.Start); !slices.Equal(got, tt.wantStart) {
				t.Errorf("start = %v, want %v", got, tt.wantStart)
			}
			if got := ids(st.Human); !slices.Equal(got, tt.wantHuman) {
				t.Errorf("human = %v, want %v", got, tt.wantHuman)
			}
		})
	}
}

func TestDrift(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	day := int64(24 * 60 * 60)
	at := func(daysAgo int64, subject string) string {
		return strconv.FormatInt(now.Unix()-daysAgo*day, 10) + "\t" + subject
	}
	tests := []struct {
		name string
		log  []string
		want string
	}{
		{"never run", []string{at(1, "A-1: x (#1)"), at(1, "A-1: status built")}, "none yet, 1 merges · due true"},
		{"recent", []string{at(0, "A-2: y (#3)"), at(2, "drift: week 1 (#2)")}, "1 merges and 2 days since the last one · due false"},
		{"a week old", []string{at(8, "drift: week 1 (#2)")}, "0 merges and 8 days since the last one · due true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := drift(tt.log, now); !strings.HasSuffix(got, tt.want) {
				t.Errorf("drift = %q, want it to end with %q", got, tt.want)
			}
		})
	}
}

func TestTickMarksMergedTickets(t *testing.T) {
	text, ticked, err := tick(fixture, map[string]bool{"A-1": true, "A-3": true, "ci": true})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !slices.Equal(ticked, []string{"A-3"}) {
		t.Errorf("ticked = %v, want [A-3]: A-1 was already ticked and ci is not a ticket", ticked)
	}
	if !strings.Contains(text, "- [x] **A-3** Third") || strings.Count(text, "- [x]") != 2 {
		t.Errorf("plan after tick:\n%s\nwant A-1 and A-3 ticked, nothing else", text)
	}
}

// The real plan must always parse: a malformed line would hide a ticket.
func TestBuildPlanParses(t *testing.T) {
	b, err := os.ReadFile("../../" + planPath)
	if err != nil {
		t.Fatalf("read %s: %v", planPath, err)
	}
	plan, err := parsePlan(string(b))
	if err != nil {
		t.Fatalf("parse %s: %v", planPath, err)
	}
	if len(plan) < 100 {
		t.Errorf("%s has %d ticket lines, want the whole plan (100 or more)", planPath, len(plan))
	}
}
