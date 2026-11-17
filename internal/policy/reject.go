package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Violation is one denied field of a rejected spec.
type Violation struct {
	Field string
	Key   string
	Value string
}

// Rejection is the error returned when Check or CheckCompose denies a spec.
type Rejection struct {
	byService  map[string][]Violation
	Violations []Violation
	spec       Spec
}

func (r *Rejection) Error() string {
	if r == nil {
		return "container policy rejected the spec"
	}
	if len(r.byService) > 0 {
		var names []string
		for n := range r.byService {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		for i, n := range names {
			if i > 0 {
				b.WriteByte('\n')
			}
			spec := r.spec
			spec.Service = n

			b.WriteString(renderRejection(spec, r.byService[n]))
		}
		return b.String()
	}
	return renderRejection(r.spec, r.Violations)
}

func renderRejection(spec Spec, vs []Violation) string {
	sorted := append([]Violation(nil), vs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Field < sorted[j].Field })

	var b strings.Builder
	svc := spec.Service
	proj := spec.Project
	fmt.Fprintf(&b, "container policy rejected service %q (project %q):\n", svc, proj)
	for _, v := range sorted {
		fmt.Fprintf(&b, "  %s is not allowed\n", v.Field)
	}

	type pair struct{ key, value string }
	var lines []pair
	seen := map[string]bool{}
	var noSnippet bool
	for _, v := range sorted {
		if v.Key == "" {
			continue
		}
		// A host sysctl (no block can allow) has no snippet.
		if strings.Contains(v.Field, "no block can allow") || strings.HasPrefix(v.Field, "sysctls:") && strings.Contains(v.Field, "no block can allow") {
			noSnippet = true
			continue
		}
		if v.Value == "" {
			continue
		}
		if seen[v.Key] {
			continue
		}
		seen[v.Key] = true
		lines = append(lines, pair{v.Key, v.Value})
	}
	// Detect "no block can allow" from Field text used below; if every remaining
	// violation is a never-allow sysctl, omit the snippet.
	allNever := true
	for _, v := range sorted {
		if !strings.Contains(v.Field, "no block can allow") && v.Key != "" && v.Value != "" {
			allNever = false
			break
		}
	}
	if len(lines) == 0 || allNever {
		_ = noSnippet
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "To allow these, add to %s:\n\n", agentTOMLPath)
	fmt.Fprintf(&b, "  [[containers.allow]]\n")
	fmt.Fprintf(&b, "  %-12s= %q\n", "project", proj)
	fmt.Fprintf(&b, "  %-12s= %q\n", "image", digestForm(spec.Image))
	for _, l := range lines {
		if l.value == "true" {
			fmt.Fprintf(&b, "  %-12s= true\n", l.key)
		} else {
			fmt.Fprintf(&b, "  %-12s= %s\n", l.key, l.value)
		}
	}
	fmt.Fprintf(&b, "\nThen restart the agent. The file is read at start-up only.")
	return b.String()
}
