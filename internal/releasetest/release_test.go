// Package releasetest holds the acceptance tests for .github/workflows/release.yml.
// It has no runtime code.
//
// The workflow runs only on a version tag, so make ci never executes it. These
// tests read it as text from the repository root with the standard library
// alone: no YAML module is imported (W7-6c, docs/specs/update.md §9). They need
// no network, Docker, gh or GitHub runner, and are safe in two worktrees at once.
package releasetest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	workflowPath = ".github/workflows/release.yml"
	ciPath       = ".github/workflows/ci.yml"

	sbomAction   = "anchore/sbom-action"
	attestAction = "actions/attest-build-provenance"

	sbomPin   = sbomAction + "@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3"
	attestPin = attestAction + "@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2"

	tagPattern = "v[0-9]+.[0-9]+.[0-9]+"
	versionEnv = "${{ github.ref_name }}"
)

var (
	pinRe     = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+@[0-9a-f]{40} # v[0-9]\S*$`)
	keyRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+:`)
	copyRe    = regexp.MustCompile(`(?m)^cp\s+deploy/systemd/moonglass-\*\s+["']?dist/\$ARCH/?["']?$`)
	sumRe     = regexp.MustCompile(`(?m)^sha256sum\s+moonglass\s+moonglass\.sbom\.json\s+moonglass-\*\s*>\s*SHA256SUMS$`)
	tarRe     = regexp.MustCompile(`(?m)^tar\s+-cf\s+["']?\.\./moonglass-\$VERSION-linux-\$ARCH\.tar["']?\s+--\s+\*\s*$`)
	ldflagsRe = regexp.MustCompile(`-ldflags\s+(?:"([^"]*)"|'([^']*)'|(\S+))`)
	outputRe  = regexp.MustCompile(`-o\s+(\S+)`)
	createRe  = regexp.MustCompile(`gh\s+release\s+create\s+\S*\$VERSION\S*\s+[^\n]*--draft`)
	cdRe      = regexp.MustCompile(`(?m)^cd\s+["']?dist/\$ARCH/?["']?\s*$`)
	anyCdRe   = regexp.MustCompile(`(?m)^cd\s`)
	awsRe     = regexp.MustCompile(`(?i)\baws\b`)
	envKeyRe  = regexp.MustCompile(`^\s*environment:`)
)

// node is one entry of the workflow's indentation tree: a mapping key with its
// inline value, or a sequence item when key is empty.
type node struct {
	key   string
	value string
	line  int
	kids  []*node
}

type sourceLine struct {
	num  int
	text string
}

// step is one entry of the workflow's steps: list.
type step struct {
	name string
	uses string
	run  string
	with map[string]string
	env  map[string]string
	line int
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v (the file must exist at the repository root)", rel, err)
	}
	return string(data)
}

func workflowText(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, workflowPath)
}

func indentOf(t *testing.T, path string, l sourceLine) int {
	t.Helper()
	n := 0
	for n < len(l.text) && l.text[n] == ' ' {
		n++
	}
	if n < len(l.text) && l.text[n] == '\t' {
		t.Fatalf("%s:%d: tab indentation, want spaces", path, l.num)
	}
	return n
}

func isSeqItem(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "- ") || trimmed == "-"
}

func isMappingEntry(content string) bool { return keyRe.MatchString(content) }

func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseWorkflow builds an indentation tree for the YAML subset the workflow
// uses: block mappings, block sequences, inline scalars and block scalars.
func parseWorkflow(t *testing.T, path, text string) []*node {
	t.Helper()
	var lines []sourceLine
	for i, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, sourceLine{num: i + 1, text: raw})
	}
	if len(lines) == 0 {
		t.Fatalf("%s: empty file, want a workflow", path)
	}
	idx := 0
	nodes := parseMapping(t, path, lines, &idx, indentOf(t, path, lines[0]), nil)
	if idx != len(lines) {
		t.Fatalf("%s:%d: unexpected line %q", path, lines[idx].num, strings.TrimSpace(lines[idx].text))
	}
	return nodes
}

func parseMapping(t *testing.T, path string, lines []sourceLine, idx *int, indent int, first *sourceLine) []*node {
	t.Helper()
	var out []*node
	if first != nil {
		out = append(out, parseEntry(t, path, lines, idx, indent, *first, false))
	}
	for *idx < len(lines) {
		l := lines[*idx]
		ind := indentOf(t, path, l)
		if ind < indent {
			break
		}
		if ind > indent {
			t.Fatalf("%s:%d: indentation %d, want %d", path, l.num, ind, indent)
		}
		if isSeqItem(l.text) {
			break
		}
		out = append(out, parseEntry(t, path, lines, idx, indent, l, true))
	}
	return out
}

// parseEntry reads one mapping entry. When advance is false, l describes a
// sequence item's first key and lines[*idx] is already the line after it.
func parseEntry(t *testing.T, path string, lines []sourceLine, idx *int, indent int, l sourceLine, advance bool) *node {
	t.Helper()
	content := strings.TrimSpace(l.text)
	rawKey, rest, ok := strings.Cut(content, ":")
	key := unquote(strings.TrimSpace(rawKey))
	if !ok || key == "" || strings.ContainsAny(key, " \t") {
		t.Fatalf("%s:%d: %q, want key: value", path, l.num, content)
	}
	n := &node{key: key, line: l.num}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "#") {
		rest = ""
	}
	if advance {
		*idx = *idx + 1
	}
	switch {
	case rest == "":
		if *idx < len(lines) && indentOf(t, path, lines[*idx]) > indent {
			childIndent := indentOf(t, path, lines[*idx])
			if isSeqItem(lines[*idx].text) {
				n.kids = parseSequence(t, path, lines, idx, childIndent)
			} else {
				n.kids = parseMapping(t, path, lines, idx, childIndent, nil)
			}
		}
	case strings.HasPrefix(rest, "|") || strings.HasPrefix(rest, ">"):
		n.value = parseBlockScalar(t, path, lines, idx, indent)
	default:
		n.value = rest
	}
	return n
}

func parseSequence(t *testing.T, path string, lines []sourceLine, idx *int, indent int) []*node {
	t.Helper()
	var out []*node
	for *idx < len(lines) {
		l := lines[*idx]
		if indentOf(t, path, l) != indent || !isSeqItem(l.text) {
			break
		}
		content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l.text), "-"))
		item := &node{line: l.num}
		*idx = *idx + 1
		switch {
		case content == "":
			if *idx < len(lines) && indentOf(t, path, lines[*idx]) > indent {
				childIndent := indentOf(t, path, lines[*idx])
				if isSeqItem(lines[*idx].text) {
					item.kids = parseSequence(t, path, lines, idx, childIndent)
				} else {
					item.kids = parseMapping(t, path, lines, idx, childIndent, nil)
				}
			}
		case isMappingEntry(content):
			item.kids = parseMapping(t, path, lines, idx, indent+2, &sourceLine{num: l.num, text: content})
		default:
			item.value = unquote(content)
		}
		out = append(out, item)
	}
	return out
}

func parseBlockScalar(t *testing.T, path string, lines []sourceLine, idx *int, parentIndent int) string {
	t.Helper()
	blockIndent := -1
	var out []string
	for *idx < len(lines) {
		l := lines[*idx]
		ind := indentOf(t, path, l)
		if ind <= parentIndent {
			break
		}
		if blockIndent < 0 {
			blockIndent = ind
		}
		if ind < blockIndent {
			t.Fatalf("%s:%d: block indentation %d, want %d", path, l.num, ind, blockIndent)
		}
		out = append(out, l.text[blockIndent:])
		*idx = *idx + 1
	}
	return strings.Join(out, "\n")
}

// parseSteps parses the release workflow's steps: it has one job, so exactly
// one steps: key in the whole file.
func parseSteps(t *testing.T, text string) []step {
	t.Helper()
	if n := len(findNodes(parseWorkflow(t, workflowPath, text), "steps")); n != 1 {
		t.Fatalf("%s: %d steps: keys, want exactly 1", workflowPath, n)
	}
	return parseStepsAt(t, workflowPath, text, "release")
}

// parseStepsAt parses the steps: of jobs.<job> in the workflow at path. Other
// jobs in the same file are not read.
func parseStepsAt(t *testing.T, path, text, job string) []step {
	t.Helper()
	root := parseWorkflow(t, path, text)
	sn := pathNode(t, path, root, "jobs", job, "steps")
	if sn.value != "" || len(sn.kids) == 0 {
		t.Fatalf("%s:%d: steps: %q, want a non-empty list", path, sn.line, sn.value)
	}
	var out []step
	for _, item := range sn.kids {
		if item.key != "" || len(item.kids) == 0 {
			t.Fatalf("%s:%d: step is not a mapping", path, item.line)
		}
		s := step{line: item.line, with: map[string]string{}, env: map[string]string{}}
		for _, k := range item.kids {
			switch k.key {
			case "name":
				s.name = k.value
			case "uses":
				s.uses = k.value
			case "run":
				s.run = k.value
			case "with", "env":
				m := s.with
				if k.key == "env" {
					m = s.env
				}
				if k.value != "" || len(k.kids) == 0 {
					t.Fatalf("%s:%d: %s: %q, want a mapping of keys to values", path, k.line, k.key, k.value)
				}
				for _, e := range k.kids {
					if e.key == "" || len(e.kids) != 0 {
						t.Fatalf("%s:%d: %s entry is not a key: value", path, e.line, k.key)
					}
					m[e.key] = e.value
				}
			default:
				t.Fatalf("%s:%d: unknown step key %q, want one of name, uses, run, with, env", path, k.line, k.key)
			}
		}
		if s.uses == "" && s.run == "" {
			t.Fatalf("%s:%d: step %q has neither uses nor run", path, item.line, s.name)
		}
		out = append(out, s)
	}
	return out
}

func findNodes(nodes []*node, key string) []*node {
	var out []*node
	var walk func([]*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			if n.key == key {
				out = append(out, n)
			}
			walk(n.kids)
		}
	}
	walk(nodes)
	return out
}

// pathNode walks keys from the top level, wanting each key exactly once among
// its parent's children.
func pathNode(t *testing.T, path string, nodes []*node, keys ...string) *node {
	t.Helper()
	var n *node
	for i, key := range keys {
		var found []*node
		for _, c := range nodes {
			if c.key == key {
				found = append(found, c)
			}
		}
		if len(found) != 1 {
			t.Fatalf("%s: %d %s: keys, want exactly 1", path, len(found), strings.Join(keys[:i+1], "."))
		}
		n = found[0]
		nodes = n.kids
	}
	return n
}

func onlyNode(t *testing.T, nodes []*node, key string) *node {
	t.Helper()
	var found []*node
	for _, n := range nodes {
		if n.key == key {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		lines := make([]int, 0, len(found))
		for _, n := range found {
			lines = append(lines, n.line)
		}
		t.Fatalf("%s: %d %s: keys at lines %v, want exactly 1", workflowPath, len(found), key, lines)
	}
	return found[0]
}

func sequenceValues(t *testing.T, n *node) []string {
	t.Helper()
	if len(n.kids) == 0 {
		if n.value == "" {
			return nil
		}
		var out []string
		for _, field := range strings.Split(n.value, ",") {
			out = append(out, unquote(strings.TrimSpace(strings.Trim(field, "[]"))))
		}
		return out
	}
	var out []string
	for _, k := range n.kids {
		if k.key != "" || len(k.kids) != 0 {
			t.Fatalf("%s:%d: sequence item %q, want a scalar", workflowPath, k.line, k.value)
		}
		out = append(out, k.value)
	}
	return out
}

func stepsByEnv(steps []step, key, value string) []step {
	var out []step
	for _, s := range steps {
		if s.env[key] == value {
			out = append(out, s)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// shellTokens splits one run script line into shell words, dropping the quotes
// that surround a word and keeping their contents literal. It fails the test on
// an unterminated quote so a malformed script never passes silently.
func shellTokens(t *testing.T, script string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	var quote byte
	inWord := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		t.Fatalf("%s: run script %q has an unterminated quote, want balanced quotes", workflowPath, script)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// createOperands splits a gh release create command into its tag and file
// operands, skipping flags and the values they consume.
func createOperands(t *testing.T, tokens []string) (tag string, files []string) {
	t.Helper()
	if len(tokens) < 4 || tokens[0] != "gh" || tokens[1] != "release" || tokens[2] != "create" {
		t.Fatalf("%s: run tokens %v, want a gh release create command", workflowPath, tokens)
	}
	valueFlags := map[string]bool{
		"--title": true, "--notes": true, "--notes-file": true, "--target": true,
		"--repo": true, "--discussion-category": true,
		"-t": true, "-n": true, "-F": true, "-R": true,
	}
	var operands []string
	for i := 3; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case strings.HasPrefix(tok, "--") && strings.Contains(tok, "="):
			continue
		case valueFlags[tok]:
			i++
		case strings.HasPrefix(tok, "-"):
			continue
		default:
			operands = append(operands, tok)
		}
	}
	if len(operands) == 0 {
		t.Fatalf("%s: gh release create has no operands, want a tag and the two tars", workflowPath)
	}
	return operands[0], operands[1:]
}

func firstLineContaining(text, want string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, want) {
			return i + 1
		}
	}
	return 0
}

func firstLineMatching(text string, re *regexp.Regexp) int {
	for i, line := range strings.Split(text, "\n") {
		if re.MatchString(line) {
			return i + 1
		}
	}
	return 0
}

// bytePositions lists where each regex match starts, for failure messages.
func bytePositions(matches [][]int) []int {
	out := make([]int, len(matches))
	for i, m := range matches {
		out[i] = m[0]
	}
	return out
}

func isSBOMOrPackage(s step) bool {
	return strings.HasPrefix(s.uses, sbomAction+"@") || s.env["ARCH"] != ""
}

func pinFor(t *testing.T, path string, steps []step, action string) string {
	t.Helper()
	var found []string
	for _, s := range steps {
		if strings.HasPrefix(s.uses, action+"@") {
			found = append(found, s.uses)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: %d uses of %s, want exactly 1", path, len(found), action)
	}
	return found[0]
}

func TestReleaseWorkflowTriggersOnVersionTagsOnly(t *testing.T) {
	text := workflowText(t)
	root := parseWorkflow(t, workflowPath, text)

	on := onlyNode(t, root, "on")
	if on.value != "" {
		t.Errorf("%s:%d: on: %q, want a push trigger with tags", workflowPath, on.line, on.value)
	}
	gotTriggers := make([]string, 0, len(on.kids))
	for _, k := range on.kids {
		gotTriggers = append(gotTriggers, k.key)
	}
	if len(on.kids) != 1 || on.kids[0].key != "push" {
		t.Fatalf("%s:%d: on: triggers %v, want exactly [push]", workflowPath, on.line, gotTriggers)
	}
	push := on.kids[0]
	if push.value != "" {
		t.Errorf("%s:%d: push: %q, want a mapping with tags", workflowPath, push.line, push.value)
	}
	for _, k := range push.kids {
		if k.key != "tags" {
			t.Errorf("%s:%d: push: has %q, want only the tags key", workflowPath, k.line, k.key)
		}
	}
	tags := onlyNode(t, push.kids, "tags")
	patterns := sequenceValues(t, tags)
	if len(patterns) != 1 || patterns[0] != tagPattern {
		t.Errorf("%s:%d: push.tags = %v, want exactly [%q]", workflowPath, tags.line, patterns, tagPattern)
	}

	for _, forbidden := range []string{"pull_request", "pull_request_target", "schedule", "workflow_run", "workflow_dispatch"} {
		for _, n := range findNodes(root, forbidden) {
			t.Errorf("%s:%d: %s: found, want no such trigger", workflowPath, n.line, forbidden)
		}
	}
}

func TestReleaseWorkflowBuildsStaticBinariesPerArch(t *testing.T) {
	text := workflowText(t)
	steps := parseSteps(t, text)
	root := parseWorkflow(t, workflowPath, text)

	versions := findNodes(root, "VERSION")
	gotVersions := make([]string, 0, len(versions))
	for _, v := range versions {
		gotVersions = append(gotVersions, v.value)
	}
	if len(versions) != 1 || versions[0].value != versionEnv {
		t.Fatalf("%s: job env VERSION = %v, want exactly [%q]", workflowPath, gotVersions, versionEnv)
	}

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			matches := stepsByEnv(steps, "GOARCH", arch)
			if len(matches) != 1 {
				t.Fatalf("%s: %d steps with env GOARCH=%s, want exactly 1", workflowPath, len(matches), arch)
			}
			s := matches[0]
			if s.run == "" {
				t.Fatalf("%s:%d: step %q has no run, want the go build command", workflowPath, s.line, s.name)
			}
			for _, want := range []string{"CGO_ENABLED=0", "GOOS=linux", "go build", "-trimpath", "./cmd/moonglass"} {
				if !strings.Contains(s.run, want) {
					t.Errorf("%s:%d: run = %q, want it to contain %q", workflowPath, s.line, s.run, want)
				}
			}
			m := ldflagsRe.FindStringSubmatch(s.run)
			if m == nil {
				t.Errorf("%s:%d: run = %q, want -ldflags holding %q", workflowPath, s.line, s.run, "-s -w -X main.version=$VERSION")
			} else {
				flags := m[1] + m[2] + m[3]
				fields := strings.Fields(flags)
				for _, want := range []string{"-s", "-w"} {
					if !slices.Contains(fields, want) {
						t.Errorf("%s:%d: -ldflags = %q, want it to hold %q", workflowPath, s.line, flags, want)
					}
				}
				if want := "-X main.version=$VERSION"; !strings.Contains(flags, want) {
					t.Errorf("%s:%d: -ldflags = %q, want it to hold %q", workflowPath, s.line, flags, want)
				}
			}
			wantOut := "dist/" + arch + "/moonglass"
			om := outputRe.FindStringSubmatch(s.run)
			gotOut := ""
			if om != nil {
				gotOut = strings.Trim(om[1], `"'`)
			}
			if om == nil || gotOut != wantOut {
				t.Errorf("%s:%d: go build output = %q, want %q", workflowPath, s.line, gotOut, wantOut)
			}
		})
	}

	for _, s := range steps {
		if strings.Contains(s.run, "GOARCH=") {
			t.Errorf("%s:%d: run sets GOARCH in the script, want it set through env GOARCH", workflowPath, s.line)
		}
	}
}

func TestReleaseWorkflowWritesSPDXSBOM(t *testing.T) {
	steps := parseSteps(t, workflowText(t))
	keys := []string{"file", "format", "output-file", "upload-artifact", "upload-release-assets"}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			want := map[string]string{
				"file":                  "dist/" + arch + "/moonglass",
				"format":                "spdx-json",
				"output-file":           "dist/" + arch + "/moonglass.sbom.json",
				"upload-artifact":       "false",
				"upload-release-assets": "false",
			}
			var matches []step
			for _, s := range steps {
				if strings.HasPrefix(s.uses, sbomAction+"@") && s.with["file"] == want["file"] {
					matches = append(matches, s)
				}
			}
			if len(matches) != 1 {
				t.Fatalf("%s: %d %s steps with with.file=%q, want exactly 1", workflowPath, len(matches), sbomAction, want["file"])
			}
			s := matches[0]
			for _, key := range keys {
				if got := s.with[key]; got != want[key] {
					t.Errorf("%s:%d: %s with.%s = %q, want %q", workflowPath, s.line, s.name, key, got, want[key])
				}
			}
		})
	}
}

func TestReleaseWorkflowPackagesArtifactSet(t *testing.T) {
	text := workflowText(t)
	steps := parseSteps(t, text)

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			buildIdx := -1
			for i, s := range steps {
				if s.env["GOARCH"] != arch {
					continue
				}
				if buildIdx >= 0 {
					t.Fatalf("%s: steps at lines %d and %d set env GOARCH=%s, want exactly 1", workflowPath, steps[buildIdx].line, s.line, arch)
				}
				buildIdx = i
			}
			if buildIdx < 0 {
				t.Fatalf("%s: no step sets env GOARCH=%s, want the build step before the package step", workflowPath, arch)
			}

			sbomWanted := "dist/" + arch + "/moonglass.sbom.json"
			sbomIdx := -1
			for i, s := range steps {
				if !strings.HasPrefix(s.uses, sbomAction+"@") || s.with["output-file"] != sbomWanted {
					continue
				}
				if sbomIdx >= 0 {
					t.Fatalf("%s: SBOM steps at lines %d and %d write %s, want exactly 1", workflowPath, steps[sbomIdx].line, s.line, sbomWanted)
				}
				sbomIdx = i
			}
			if sbomIdx < 0 {
				t.Fatalf("%s: no %s step writes %s, want the SBOM before the package step", workflowPath, sbomAction, sbomWanted)
			}

			pkgIdx := -1
			for i, s := range steps {
				if s.env["ARCH"] != arch {
					continue
				}
				if pkgIdx >= 0 {
					t.Fatalf("%s: steps at lines %d and %d set env ARCH=%s, want exactly 1", workflowPath, steps[pkgIdx].line, s.line, arch)
				}
				pkgIdx = i
			}
			if pkgIdx < 0 {
				t.Fatalf("%s: no step sets env ARCH=%s, want the package step", workflowPath, arch)
			}

			if buildIdx >= sbomIdx {
				t.Errorf("%s: build step at line %d, SBOM step at line %d, want the build before the SBOM", workflowPath, steps[buildIdx].line, steps[sbomIdx].line)
			}
			if sbomIdx >= pkgIdx {
				t.Errorf("%s: SBOM step at line %d, package step at line %d, want the SBOM before the package so %s exists", workflowPath, steps[sbomIdx].line, steps[pkgIdx].line, sbomWanted)
			}

			s := steps[pkgIdx]
			if s.run == "" {
				t.Fatalf("%s:%d: step %q has no run, want the copy, cd, sha256sum and tar lines", workflowPath, s.line, s.name)
			}
			copyAt := copyRe.FindStringIndex(s.run)
			cdAt := cdRe.FindStringIndex(s.run)
			sumAt := sumRe.FindStringIndex(s.run)
			tarAt := tarRe.FindStringIndex(s.run)
			cds := anyCdRe.FindAllStringIndex(s.run, -1)
			if copyAt == nil {
				t.Errorf("%s:%d: run = %q, want a line %q", workflowPath, s.line, s.run, `cp deploy/systemd/moonglass-* "dist/$ARCH/"`)
			}
			if cdAt == nil {
				t.Errorf("%s:%d: run = %q, want a line %q", workflowPath, s.line, s.run, `cd "dist/$ARCH"`)
			}
			if len(cds) > 1 {
				t.Errorf("%s:%d: run = %q has %d cd lines at byte offsets %v, want only the one into dist/$ARCH so sha256sum and tar see the arch set", workflowPath, s.line, s.run, len(cds), bytePositions(cds))
			}
			if sumAt == nil {
				t.Errorf("%s:%d: run = %q, want a line %q", workflowPath, s.line, s.run, "sha256sum moonglass moonglass.sbom.json moonglass-* > SHA256SUMS")
			}
			if tarAt == nil {
				t.Errorf("%s:%d: run = %q, want a line %q", workflowPath, s.line, s.run, `tar -cf "../moonglass-$VERSION-linux-$ARCH.tar" -- *`)
			}
			if copyAt != nil && cdAt != nil && sumAt != nil && tarAt != nil {
				order := []struct {
					name string
					at   int
				}{
					{"cp", copyAt[0]},
					{"cd", cdAt[0]},
					{"sha256sum", sumAt[0]},
					{"tar", tarAt[0]},
				}
				for i := 1; i < len(order); i++ {
					if order[i-1].at >= order[i].at {
						t.Errorf("%s:%d: run order is cp at %d, cd at %d, sha256sum at %d, tar at %d, want cp then cd then sha256sum then tar", workflowPath, s.line, copyAt[0], cdAt[0], sumAt[0], tarAt[0])
						break
					}
				}
			}
		})
	}

	for i, line := range strings.Split(text, "\n") {
		for _, suffix := range []string{".service", ".timer"} {
			if strings.Contains(line, suffix) {
				t.Errorf("%s:%d: line %q names a single unit, want the glob deploy/systemd/moonglass-*", workflowPath, i+1, strings.TrimSpace(line))
			}
		}
	}

	for _, s := range steps {
		for _, line := range splitLines(s.run) {
			if strings.Contains(line, "SHA256SUMS") && !sumRe.MatchString(line) {
				t.Errorf("%s:%d: run line %q mentions SHA256SUMS, want only %q per arch", workflowPath, s.line, line, "sha256sum moonglass moonglass.sbom.json moonglass-* > SHA256SUMS")
			}
		}
	}
}

func TestReleaseWorkflowAttestsBinariesAndSBOM(t *testing.T) {
	steps := parseSteps(t, workflowText(t))

	var attest []int
	for i, s := range steps {
		if strings.HasPrefix(s.uses, attestAction+"@") {
			attest = append(attest, i)
		}
	}
	if len(attest) != 1 {
		t.Fatalf("%s: %d %s steps, want exactly 1", workflowPath, len(attest), attestAction)
	}
	idx := attest[0]
	s := steps[idx]

	want := []string{
		"dist/amd64/moonglass",
		"dist/amd64/moonglass.sbom.json",
		"dist/arm64/moonglass",
		"dist/arm64/moonglass.sbom.json",
	}
	got := splitLines(s.with["subject-path"])
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s:%d: attest subject-path = %v, want %v", workflowPath, s.line, got, want)
	}

	lastSBOMOrPackage := -1
	lastLine := 0
	lastName := ""
	for j, other := range steps {
		if j == idx || !isSBOMOrPackage(other) {
			continue
		}
		if j > lastSBOMOrPackage {
			lastSBOMOrPackage, lastLine, lastName = j, other.line, other.name
		}
	}
	if lastSBOMOrPackage > idx {
		t.Errorf("%s:%d: attest step comes before %q at line %d, want it after both SBOM and both package steps", workflowPath, s.line, lastName, lastLine)
	}

	upload := -1
	for j, other := range steps {
		if strings.Contains(other.run, "gh release") {
			upload = j
			break
		}
	}
	if upload < 0 {
		t.Fatalf("%s: no step runs gh release, want the attest step before it", workflowPath)
	}
	if idx > upload {
		t.Errorf("%s:%d: attest step comes after the gh release step at line %d, want it before", workflowPath, s.line, steps[upload].line)
	}
}

func TestReleaseWorkflowUploadsToDraftRelease(t *testing.T) {
	text := workflowText(t)
	steps := parseSteps(t, text)

	var uploads []step
	for _, s := range steps {
		if strings.Contains(s.run, "gh release") {
			uploads = append(uploads, s)
		}
	}
	if len(uploads) != 1 {
		t.Fatalf("%s: %d steps run gh release, want exactly 1", workflowPath, len(uploads))
	}
	s := uploads[0]
	if !createRe.MatchString(s.run) {
		t.Errorf("%s:%d: run = %q, want %q", workflowPath, s.line, s.run, `gh release create "$VERSION" --draft ...`)
	}
	tokens := shellTokens(t, s.run)
	tag, files := createOperands(t, tokens)
	if tag != "$VERSION" {
		t.Errorf("%s:%d: gh release create tag = %q, want %q", workflowPath, s.line, tag, "$VERSION")
	}
	got := slices.Clone(files)
	slices.Sort(got)
	want := []string{
		"dist/moonglass-$VERSION-linux-amd64.tar",
		"dist/moonglass-$VERSION-linux-arm64.tar",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s:%d: gh release create files = %v, want exactly %v", workflowPath, s.line, got, want)
	}
	draft := false
	for _, tok := range tokens {
		if tok == "--draft" || tok == "--draft=true" {
			draft = true
		}
	}
	if !draft {
		t.Errorf("%s:%d: run tokens %v, want --draft so the release stays a draft", workflowPath, s.line, tokens)
	}

	for _, forbidden := range []string{"--latest", "--draft=false", "gh release edit", "gh release upload", "gh api", "softprops/", "upload-release-assets: true"} {
		if line := firstLineContaining(text, forbidden); line != 0 {
			t.Errorf("%s:%d: line contains %q, want none", workflowPath, line, forbidden)
		}
	}
}

func TestReleaseWorkflowPinsEveryAction(t *testing.T) {
	text := workflowText(t)
	root := parseWorkflow(t, workflowPath, text)
	ciSteps := parseStepsAt(t, ciPath, readRepoFile(t, ciPath), "ci")

	usesNodes := findNodes(root, "uses")
	if len(usesNodes) == 0 {
		t.Fatalf("%s: no uses: key, want the four pinned actions", workflowPath)
	}
	var got []string
	for _, n := range usesNodes {
		got = append(got, n.value)
		if !pinRe.MatchString(n.value) {
			t.Errorf("%s:%d: uses = %q, want owner/repo@<40 lowercase hex> # vN...", workflowPath, n.line, n.value)
		}
	}

	checkoutPin := pinFor(t, ciPath, ciSteps, "actions/checkout")
	setupGoPin := pinFor(t, ciPath, ciSteps, "actions/setup-go")
	want := []string{checkoutPin, setupGoPin, sbomPin, attestPin}
	counts := map[string]int{}
	for _, g := range got {
		counts[g]++
		if !slices.Contains(want, g) {
			t.Errorf("%s: uses %q, want one of %v", workflowPath, g, want)
		}
	}
	for _, w := range want {
		if counts[w] == 0 {
			t.Errorf("%s: uses %q missing, want the pin from %s or docs/specs/update.md §9", workflowPath, w, ciPath)
		}
	}
	if counts[checkoutPin] != 1 {
		t.Errorf("%s: %d uses of %q, want exactly 1", workflowPath, counts[checkoutPin], checkoutPin)
	}
	if counts[setupGoPin] != 1 {
		t.Errorf("%s: %d uses of %q, want exactly 1", workflowPath, counts[setupGoPin], setupGoPin)
	}
	if counts[attestPin] != 1 {
		t.Errorf("%s: %d uses of %q, want exactly 1", workflowPath, counts[attestPin], attestPin)
	}

	steps := parseSteps(t, text)
	for _, s := range steps {
		switch s.uses {
		case checkoutPin:
			if got := s.with["persist-credentials"]; got != "false" {
				t.Errorf("%s:%d: actions/checkout with.persist-credentials = %q, want \"false\"", workflowPath, s.line, got)
			}
		case setupGoPin:
			if got := s.with["go-version-file"]; got != "go.mod" {
				t.Errorf("%s:%d: actions/setup-go with.go-version-file = %q, want \"go.mod\"", workflowPath, s.line, got)
			}
		}
	}
}

func TestReleaseWorkflowGrantsOnlyReleasePermissions(t *testing.T) {
	root := parseWorkflow(t, workflowPath, workflowText(t))

	blocks := findNodes(root, "permissions")
	if len(blocks) == 0 {
		t.Fatalf("%s: no permissions: block, want id-token: write, attestations: write and contents: write", workflowPath)
	}
	allowed := map[string]bool{"id-token": true, "attestations": true, "contents": true}
	granted := map[string]bool{}
	for _, b := range blocks {
		if b.value != "" {
			t.Errorf("%s:%d: permissions: %q, want a mapping of id-token, attestations and contents", workflowPath, b.line, b.value)
			continue
		}
		if len(b.kids) == 0 {
			t.Errorf("%s:%d: permissions: is empty, want at least id-token: write", workflowPath, b.line)
			continue
		}
		for _, e := range b.kids {
			if !allowed[e.key] {
				t.Errorf("%s:%d: permissions grants %q, want only id-token, attestations or contents", workflowPath, e.line, e.key)
				continue
			}
			if e.value != "write" {
				t.Errorf("%s:%d: permissions %s = %q, want \"write\"", workflowPath, e.line, e.key, e.value)
				continue
			}
			granted[e.key] = true
		}
	}
	for _, key := range []string{"id-token", "attestations", "contents"} {
		if !granted[key] {
			t.Errorf("%s: permissions never grants %s: write", workflowPath, key)
		}
	}
}

func TestReleaseWorkflowHoldsNoSecretOrSigningStep(t *testing.T) {
	text := workflowText(t)
	steps := parseSteps(t, text)

	for _, forbidden := range []string{"secrets.", "ssh-keygen", ".sig", "workflow_dispatch"} {
		if line := firstLineContaining(text, forbidden); line != 0 {
			t.Errorf("%s:%d: line contains %q, want none", workflowPath, line, forbidden)
		}
	}
	if line := firstLineMatching(text, envKeyRe); line != 0 {
		t.Errorf("%s:%d: line names an environment:, want none", workflowPath, line)
	}
	if line := firstLineMatching(text, awsRe); line != 0 {
		t.Errorf("%s:%d: line contains \"aws\", want none", workflowPath, line)
	}
	for _, s := range steps {
		if strings.Contains(s.run, "${{") {
			t.Errorf("%s:%d: run = %q, want no ${{ }} expression in a run script", workflowPath, s.line, s.run)
		}
	}
}
