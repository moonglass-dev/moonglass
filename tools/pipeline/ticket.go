package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	planPath     = "docs/plan/build-plan.md"
	templatePath = "docs/tickets/TEMPLATE.md"
)

func ticketPath(id string) string { return "docs/tickets/" + id + ".md" }

func ticketDir(id string) string { return "docs/tickets/" + id + "/" }

var (
	idRe       = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9A-Z]+[a-z]?$`)
	planLineRe = regexp.MustCompile(`^- \*\*([^*]+)\*\* (.+?) — (.+)$`)
	testRe     = regexp.MustCompile(`^\./[A-Za-z0-9_./-]+ (Test|Fuzz)[A-Za-z0-9_]*$`)
)

var (
	risks    = []string{"low", "standard", "high"}
	kinds    = []string{"build", "review", "human"}
	statuses = []string{"todo", "planned", "tests-locked", "built", "in-review", "merged", "blocked", "needs-human"}
)

// planTicket is one line of the build plan.
type planTicket struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Intent    string   `json:"intent"`
	DependsOn []string `json:"depends_on"`
	Risk      string   `json:"risk"`
	Kind      string   `json:"kind"`
	After     int      `json:"after,omitempty"`
	Paths     []string `json:"paths,omitempty"`
}

// parsePlan reads every ticket line of the build plan. A line that starts
// like a ticket but does not parse is an error, never skipped.
func parsePlan(text string) ([]planTicket, error) {
	var out []planTicket
	seen := map[string]bool{}
	for n, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "- **") {
			continue
		}
		t, err := parsePlanLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", planPath, n+1, err)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("%s:%d: ticket %s is listed twice", planPath, n+1, t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	for _, t := range out {
		for _, d := range t.DependsOn {
			if !seen[d] {
				return nil, fmt.Errorf("%s: %s depends on %s, which is not in the plan", planPath, t.ID, d)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no ticket lines found", planPath)
	}
	return out, nil
}

func parsePlanLine(line string) (planTicket, error) {
	m := planLineRe.FindStringSubmatch(line)
	if m == nil {
		return planTicket{}, fmt.Errorf("ticket line must read `- **ID** Title — intent · depends_on: … · risk: …`: %q", line)
	}
	t := planTicket{ID: m[1], Title: strings.TrimSpace(m[2]), Kind: "build"}
	if !idRe.MatchString(t.ID) {
		return t, fmt.Errorf("bad ticket id %q", t.ID)
	}
	parts := strings.Split(m[3], " · ")
	t.Intent = strings.TrimSpace(parts[0])
	hasDeps, hasRisk := false, false
	for _, p := range parts[1:] {
		key, val, ok := strings.Cut(p, ": ")
		if !ok {
			return t, fmt.Errorf("%s: field %q is not `key: value`", t.ID, p)
		}
		val = strings.TrimSpace(val)
		switch key {
		case "depends_on":
			hasDeps = true
			t.DependsOn = splitList(val)
		case "risk":
			hasRisk = true
			t.Risk = val
		case "kind":
			t.Kind = val
		case "after":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return t, fmt.Errorf("%s: after must be a positive number of merged tickets, got %q", t.ID, val)
			}
			t.After = n
		case "paths":
			t.Paths = splitList(val)
		default:
			return t, fmt.Errorf("%s: unknown field %q", t.ID, key)
		}
	}
	switch {
	case t.Intent == "" || t.Title == "":
		return t, fmt.Errorf("%s: title and intent are required", t.ID)
	case !hasDeps:
		return t, fmt.Errorf("%s: depends_on is required (use — for none)", t.ID)
	case !hasRisk || !slices.Contains(risks, t.Risk):
		return t, fmt.Errorf("%s: risk must be one of %v", t.ID, risks)
	case !slices.Contains(kinds, t.Kind):
		return t, fmt.Errorf("%s: kind must be one of %v", t.ID, kinds)
	case t.After > 0 && t.Kind != "review":
		return t, fmt.Errorf("%s: after is only for kind: review", t.ID)
	}
	for _, d := range t.DependsOn {
		if !idRe.MatchString(d) {
			return t, fmt.Errorf("%s: bad dependency id %q", t.ID, d)
		}
	}
	return t, nil
}

func splitList(val string) []string {
	if val == "—" || val == "" {
		return []string{}
	}
	var out []string
	for _, v := range strings.Split(val, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ticket is a docs/tickets/<ID>.md file: front matter and a Markdown body.
type ticket struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Risk         string   `json:"risk"`
	DependsOn    []string `json:"depends_on"`
	AllowedPaths []string `json:"allowed_paths"`
	MaxDiffLines int      `json:"max_diff_lines"`
	Tests        []string `json:"tests"`
	Reads        []string `json:"reads"`
	Body         string   `json:"-"`
}

var ticketKeys = []string{"id", "title", "status", "risk", "depends_on", "allowed_paths", "max_diff_lines", "tests", "reads"}

// requiredSections are the headings the ticket step must write.
var requiredSections = []string{"## Scope", "## Acceptance criteria", "## Out of scope", "## Security notes"}

func splitFrontMatter(text string) (front, body string, err error) {
	if !strings.HasPrefix(text, "---\n") {
		return "", "", fmt.Errorf("must start with a --- front matter line")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return "", "", fmt.Errorf("front matter has no closing --- line")
	}
	return text[4 : 4+end+1], text[4+end+5:], nil
}

func parseTicket(path, text string) (ticket, error) {
	front, body, err := splitFrontMatter(text)
	if err != nil {
		return ticket{}, fmt.Errorf("%s: %w", path, err)
	}
	fields, err := parseFields(front)
	if err != nil {
		return ticket{}, fmt.Errorf("%s: %w", path, err)
	}
	for k := range fields {
		if !slices.Contains(ticketKeys, k) {
			return ticket{}, fmt.Errorf("%s: unknown front matter key %q", path, k)
		}
	}
	t := ticket{
		ID:           fields["id"].scalar,
		Title:        fields["title"].scalar,
		Status:       fields["status"].scalar,
		Risk:         fields["risk"].scalar,
		DependsOn:    fields["depends_on"].list,
		AllowedPaths: fields["allowed_paths"].list,
		Tests:        fields["tests"].list,
		Reads:        fields["reads"].list,
		Body:         body,
	}
	if v := fields["max_diff_lines"].scalar; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return t, fmt.Errorf("%s: max_diff_lines must be a whole number, got %q", path, v)
		}
		t.MaxDiffLines = n
	}
	switch {
	case !idRe.MatchString(t.ID):
		return t, fmt.Errorf("%s: id %q is not a ticket id", path, t.ID)
	case path != ticketPath(t.ID) && !strings.HasSuffix(path, "/"+t.ID+".md"):
		return t, fmt.Errorf("%s: id %q does not match the file name", path, t.ID)
	case !slices.Contains(statuses, t.Status):
		return t, fmt.Errorf("%s: status must be one of %v, got %q", path, statuses, t.Status)
	case !slices.Contains(risks, t.Risk):
		return t, fmt.Errorf("%s: risk must be one of %v, got %q", path, risks, t.Risk)
	}
	for _, tt := range t.Tests {
		if !testRe.MatchString(tt) {
			return t, fmt.Errorf("%s: test %q must read `./package/path TestName`", path, tt)
		}
	}
	return t, nil
}

// complete reports what the ticket step still has to write, or "" when done.
func (t ticket) incomplete() string {
	switch {
	case strings.TrimSpace(t.Title) == "":
		return "title is empty"
	case len(t.AllowedPaths) == 0:
		return "allowed_paths is empty"
	case t.MaxDiffLines <= 0:
		return "max_diff_lines is not set"
	case t.Risk != "low" && len(t.Tests) == 0:
		return "a " + t.Risk + " ticket must name its acceptance tests"
	}
	for _, s := range requiredSections {
		if !strings.Contains(t.Body, "\n"+s+"\n") && !strings.HasPrefix(t.Body, s+"\n") {
			return "missing section " + s
		}
	}
	return ""
}

type field struct {
	scalar string
	list   []string
	isList bool
}

// parseFields reads the small YAML subset used in front matter: `key: value`,
// `key: [a, b]`, and `key:` followed by `  - item` lines.
func parseFields(front string) (map[string]field, error) {
	out := map[string]field{}
	var current string
	for n, line := range strings.Split(strings.TrimRight(front, "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if item, ok := strings.CutPrefix(strings.TrimLeft(line, " "), "- "); ok && strings.HasPrefix(line, " ") {
			if current == "" {
				return nil, fmt.Errorf("front matter line %d: list item outside a list", n+1)
			}
			f := out[current]
			f.list = append(f.list, unquote(item))
			out[current] = f
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("front matter line %d: expected `key: value`, got %q", n+1, line)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("front matter key %q appears twice", key)
		}
		current = ""
		switch {
		case val == "":
			out[key] = field{isList: true, list: []string{}}
			current = key
		case strings.HasPrefix(val, "["):
			if !strings.HasSuffix(val, "]") {
				return nil, fmt.Errorf("front matter key %q: unclosed list", key)
			}
			items := []string{}
			for _, it := range strings.Split(strings.Trim(val, "[]"), ",") {
				if it = unquote(strings.TrimSpace(it)); it != "" {
					items = append(items, it)
				}
			}
			out[key] = field{isList: true, list: items}
		default:
			out[key] = field{scalar: unquote(val)}
		}
	}
	return out, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

var statusLineRe = regexp.MustCompile(`(?m)^status: .*$`)

// withStatus rewrites only the status line of a ticket file.
func withStatus(text, status string) (string, error) {
	front, body, err := splitFrontMatter(text)
	if err != nil {
		return "", err
	}
	if !statusLineRe.MatchString(front) {
		return "", fmt.Errorf("front matter has no status line")
	}
	return "---\n" + statusLineRe.ReplaceAllString(front, "status: "+status) + "---\n" + body, nil
}

// skeleton is the ticket file a worker starts from. The ticket step fills it.
// Review tickets are complete from the start: their scope is fixed.
func skeleton(p planTicket) string {
	deps := "[]"
	if len(p.DependsOn) > 0 {
		deps = "[" + strings.Join(p.DependsOn, ", ") + "]"
	}
	if p.Kind == "review" {
		return fmt.Sprintf(`---
id: %s
title: %s
status: todo
risk: low
depends_on: %s
allowed_paths: [docs/reviews/%s.md, docs/plan/build-plan.md]
max_diff_lines: 2000
tests: []
reads: []
---

# %s

%s

## Scope

A fresh, read-only review of the whole repository, written to docs/reviews/%s.md, with one fix ticket per finding appended to the build plan. See docs/pipeline/children/arch-review.md.

## Acceptance criteria

- docs/reviews/%s.md exists and every finding has file:line evidence.
- Every finding that needs code is a new ticket line in docs/plan/build-plan.md under "Fix tickets".

## Out of scope

Changing code, specs, ADRs or the threat model.

## Security notes

Reviewed against docs/threat-model.md.
`, p.ID, p.Title, deps, p.ID, p.Title, p.Intent, p.ID, p.ID)
	}
	paths := "[]"
	if len(p.Paths) > 0 {
		paths = "[" + strings.Join(p.Paths, ", ") + "]"
	}
	return fmt.Sprintf(`---
id: %s
title: %s
status: todo
risk: %s
depends_on: %s
allowed_paths: %s
max_diff_lines: 0
tests: []
reads: []
---

# %s

%s
`, p.ID, p.Title, p.Risk, deps, paths, p.Title, p.Intent)
}
