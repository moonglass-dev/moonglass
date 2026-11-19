package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// fontURLPattern matches the url() entries of a @font-face src list.
	fontURLPattern = regexp.MustCompile(`url\(([^)]*)\)`)
	// fontStackPattern matches a --font-sans or --font-mono declaration.
	fontStackPattern = regexp.MustCompile(`--font-(sans|mono):\s*([^;]+);`)
	// orbIDPattern captures the variable holding Svelte's per-instance ID.
	orbIDPattern = regexp.MustCompile(`const (\w+) = \$props\.id\(\)`)
	// idAttributePattern matches any id= attribute whatever its quoting.
	idAttributePattern = regexp.MustCompile(`\bid=("[^"]*"|'[^']*'|\{[^}]*\}|[^\s>/]+)`)
	// spinnerAnimationPattern matches an animation declaration that never ends.
	spinnerAnimationPattern = regexp.MustCompile(`animation:[^;]*infinite`)
	// reducedMotionPattern matches the spinner's stopped animation.
	reducedMotionPattern = regexp.MustCompile(`animation:\s*none`)
)

// braceBlocks returns the body of every block that follows opener, counting
// nested braces so a rule inside a media query is returned whole. It fails the
// test when an opener has no opening or closing brace, so a malformed file
// never yields an empty result.
func braceBlocks(t *testing.T, s, opener string) []string {
	t.Helper()

	var blocks []string
	offset := 0

	for {
		index := strings.Index(s[offset:], opener)
		if index < 0 {
			break
		}
		openerStart := offset + index

		open := strings.IndexByte(s[openerStart+len(opener):], '{')
		if open < 0 {
			t.Fatalf("%q at offset %d has no opening brace, want a complete block", opener, openerStart)
		}
		bodyStart := openerStart + len(opener) + open + 1

		depth := 1
		end := -1
		for i := bodyStart; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			t.Fatalf("%q at offset %d has no matching closing brace, want a complete block", opener, openerStart)
		}

		blocks = append(blocks, s[bodyStart:end])
		offset = end + 1
	}

	return blocks
}

// braceBlock returns the body of the first block that follows opener.
func braceBlock(t *testing.T, s, opener string) string {
	t.Helper()

	blocks := braceBlocks(t, s, opener)
	if len(blocks) == 0 {
		t.Fatalf("%q not found, want a complete block", opener)
	}

	return blocks[0]
}

// declarationValue returns the value of property inside a block of CSS
// declarations, or the empty string when the property is absent.
func declarationValue(block, property string) string {
	for _, declaration := range strings.Split(block, ";") {
		name, value, ok := strings.Cut(declaration, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == property {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// srcFiles returns every file under src/ with one of the given extensions,
// keyed by its path relative to the package directory.
func srcFiles(t *testing.T, exts ...string) map[string]string {
	t.Helper()

	want := make(map[string]bool, len(exts))
	for _, ext := range exts {
		want[ext] = true
	}

	files := make(map[string]string)
	err := filepath.WalkDir("src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !want[filepath.Ext(path)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)

		return nil
	})
	if err != nil {
		t.Fatalf("walk src: %v, want every source file readable", err)
	}

	return files
}

// snippet returns a short excerpt around index so a failure quotes the text it
// complains about.
func snippet(s string, index int) string {
	const width = 40
	start := max(0, index-width/2)
	end := min(len(s), start+width)
	if end-start < width {
		start = max(0, end-width)
	}

	return strings.TrimSpace(s[start:end])
}

func TestFontUrlsLocalWOFF2(t *testing.T) {
	const fontsCSS = "src/lib/fonts.css"

	blocks := braceBlocks(t, readFile(t, fontsCSS), "@font-face")
	if len(blocks) != 2 {
		t.Fatalf("%s has %d @font-face rules, want exactly 2 (Geist and Geist Mono)", fontsCSS, len(blocks))
	}

	families := make(map[string]bool, len(blocks))
	for i, block := range blocks {
		family := strings.Trim(declarationValue(block, "font-family"), `"' `)
		families[family] = true

		if got := declarationValue(block, "font-display"); got != "swap" {
			t.Errorf("@font-face %d (%s) font-display = %q, want %q so the first paint does not shift", i+1, family, got, "swap")
		}

		src := declarationValue(block, "src")
		if src == "" {
			t.Fatalf("@font-face %d (%s) has no src declaration, want one local WOFF2 file", i+1, family)
		}
		if !strings.Contains(src, `format("woff2")`) {
			t.Errorf("@font-face %d (%s) src = %q, want the woff2 format hint", i+1, family, src)
		}

		urls := fontURLPattern.FindAllStringSubmatch(src, -1)
		if len(urls) != 1 {
			t.Fatalf("@font-face %d (%s) src = %q has %d url() entries, want exactly 1", i+1, family, src, len(urls))
		}

		url := strings.Trim(urls[0][1], `"' `)
		if strings.HasPrefix(url, "//") || strings.Contains(url, "://") || strings.HasPrefix(url, "data:") {
			t.Errorf("@font-face %d (%s) loads %q, want a local file under /fonts/ and no request to another origin", i+1, family, url)
			continue
		}
		if !strings.HasPrefix(url, "/fonts/") {
			t.Errorf("@font-face %d (%s) loads %q, want a path under /fonts/", i+1, family, url)
			continue
		}
		if !strings.HasSuffix(url, ".woff2") {
			t.Errorf("@font-face %d (%s) loads %q, want a .woff2 file", i+1, family, url)
		}

		path := filepath.Join("static", filepath.FromSlash(url))
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat %s for %q: %v, want the self-hosted font at static%s", path, url, err, url)
			continue
		}
		if info.Size() >= 1<<20 {
			t.Errorf("%s size = %d bytes, want under 1 MB so the static build budget holds", path, info.Size())
		}
		magic, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v, want the self-hosted font", path, err)
			continue
		}
		if len(magic) < 4 || string(magic[:4]) != "wOF2" {
			t.Errorf("%s does not start with the wOF2 magic bytes, want a real WOFF2 file for %q", path, url)
		}
	}

	for _, want := range []string{"Geist", "Geist Mono"} {
		if !families[want] {
			t.Errorf("%s declares families %v, want %q", fontsCSS, families, want)
		}
	}

	stacks := make(map[string]string, 2)
	for _, match := range fontStackPattern.FindAllStringSubmatch(readFile(t, "src/lib/tokens.css"), -1) {
		stacks[match[1]] = strings.TrimSpace(match[2])
	}
	for _, stack := range []struct{ kind, family, system string }{
		{"sans", "Geist", "system-ui"},
		{"mono", "Geist Mono", "ui-monospace"},
	} {
		value, ok := stacks[stack.kind]
		if !ok {
			t.Fatalf("src/lib/tokens.css has no --font-%s declaration, want a stack led by %q", stack.kind, stack.family)
		}
		first, _, _ := strings.Cut(value, ",")
		if got := strings.Trim(strings.TrimSpace(first), `"'`); got != stack.family {
			t.Errorf("--font-%s leads with %q, want %q", stack.kind, got, stack.family)
		}
		if !strings.Contains(value, stack.system) {
			t.Errorf("--font-%s = %q, want the %s system font after %q", stack.kind, value, stack.system, stack.family)
		}
	}

	const licensePath = "static/fonts/OFL.txt"
	license := readFile(t, licensePath)
	if !strings.Contains(license, "SIL Open Font License, Version 1.1") {
		t.Errorf("%s does not contain %q, want the Geist licence shipped with the fonts (OFL 1.1)", licensePath, "SIL Open Font License, Version 1.1")
	}

	for name, content := range srcFiles(t, ".css", ".html", ".svelte", ".ts", ".js") {
		for _, banned := range []string{"http://", "https://", "url(//"} {
			if index := strings.Index(content, banned); index >= 0 {
				t.Errorf("%s contains %q (near %q), want no request to another origin at runtime", name, banned, snippet(content, index))
			}
		}
		for offset := 0; ; {
			index := strings.Index(content[offset:], "@import")
			if index < 0 {
				break
			}
			start := offset + index + len("@import")
			rest := strings.TrimSpace(content[start:])
			if end := strings.IndexAny(rest, ";\n"); end >= 0 {
				rest = rest[:end]
			}
			target := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "url(")), `"' `)
			if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") &&
				(!strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//")) {
				t.Errorf("%s @import %q, want a relative path under src/", name, target)
			}
			offset = start
		}
	}

	const layoutImport = "import '$lib/fonts.css';"
	if layout := readFile(t, "src/routes/+layout.svelte"); !strings.Contains(layout, layoutImport) {
		t.Errorf("src/routes/+layout.svelte is missing %q, want the self-hosted fonts reachable from the build", layoutImport)
	}
}

func TestOrbStaticAndNoGlobalID(t *testing.T) {
	const orbPath = "src/lib/components/Orb.svelte"

	orb := readFile(t, orbPath)

	match := orbIDPattern.FindStringSubmatch(orb)
	if match == nil {
		t.Fatalf("%s has no per-instance ID declaration, want %q so two orbs never share a paint-server ID", orbPath, "const uid = $props.id()")
	}
	uid := match[1]

	ids := idAttributePattern.FindAllStringSubmatch(orb, -1)
	if len(ids) == 0 {
		t.Fatalf("%s has no id= attribute, want the gradients and clip path built from {%s}", orbPath, uid)
	}
	for _, id := range ids {
		if !strings.Contains(id[1], "{"+uid+"}") {
			t.Errorf("%s has id=%s, want every paint-server ID to contain {%s} so no global ID is hardcoded", orbPath, id[1], uid)
		}
	}

	references := 0
	for offset := 0; ; {
		index := strings.Index(orb[offset:], "url(#")
		if index < 0 {
			break
		}
		start := offset + index + len("url(#")
		end := strings.IndexByte(orb[start:], ')')
		if end < 0 {
			t.Fatalf("%s has url(#%s without a closing parenthesis, want a complete reference", orbPath, orb[start:])
		}
		reference := orb[start : start+end]
		references++
		if !strings.HasPrefix(reference, "{"+uid+"}") {
			t.Errorf("%s has url(#%s), want every reference to start with {%s} so no global paint server is used", orbPath, reference, uid)
		}
		offset = start + end + 1
	}
	if references == 0 {
		t.Fatalf("%s has no url(#...) reference, want the glass, rim and clip definitions used", orbPath)
	}

	for _, banned := range []string{"<animate", "<set", "@keyframes", "animation", "transition", "<filter", "feGaussianBlur"} {
		if index := strings.Index(orb, banned); index >= 0 {
			t.Errorf("%s contains %q (near %q), want a completely static mark with no motion and no glow (docs/specs/ui.md §5, §7)", orbPath, banned, snippet(orb, index))
		}
	}

	index := strings.Index(orb, "<svg")
	if index < 0 {
		t.Fatalf("%s has no <svg element, want the orb mark", orbPath)
	}
	end := strings.IndexByte(orb[index:], '>')
	if end < 0 {
		t.Fatalf("%s <svg element has no closing >, want well-formed markup", orbPath)
	}
	root := orb[index : index+end]
	for _, want := range []string{`width="22"`, `height="22"`} {
		if !strings.Contains(root, want) {
			t.Errorf("%s root <svg> = %q, want %s so the orb renders at 22 px", orbPath, root, want)
		}
	}
}

func TestSpinnerReducedMotion(t *testing.T) {
	const spinnerPath = "src/lib/components/Spinner.svelte"

	spinner := readFile(t, spinnerPath)

	open := strings.Index(spinner, "<style>")
	closeTag := strings.Index(spinner, "</style>")
	if open < 0 || closeTag < open {
		t.Fatalf("%s has no <style> block, want the spinner arc CSS", spinnerPath)
	}
	style := spinner[open+len("<style>") : closeTag]

	for _, want := range []string{"@keyframes", "var(--state-info)", "var(--state-warn)"} {
		if !strings.Contains(style, want) {
			t.Errorf("%s <style> is missing %q, want the building and switching spinner arc", spinnerPath, want)
		}
	}
	if !spinnerAnimationPattern.MatchString(style) {
		t.Errorf("%s <style> has no animation declaration containing %q, want the spinner to loop (docs/specs/ui.md §7)", spinnerPath, "infinite")
	}

	reduced := braceBlock(t, style, "@media (prefers-reduced-motion: reduce)")
	if !reducedMotionPattern.MatchString(reduced) {
		t.Errorf("%s prefers-reduced-motion block = %q, want an %q rule so the spinner stops", spinnerPath, strings.TrimSpace(reduced), "animation: none")
	}

	for name, content := range srcFiles(t, ".svelte", ".css") {
		if !strings.Contains(content, "@keyframes") {
			continue
		}
		if filepath.ToSlash(name) == spinnerPath {
			continue
		}
		t.Errorf("%s contains @keyframes, want only %s to animate (docs/specs/ui.md §7: one loop)", name, spinnerPath)
	}
}
