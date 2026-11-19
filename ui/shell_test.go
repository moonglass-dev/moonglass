package ui

import (
	"regexp"
	"strings"
	"testing"
)

// The 2-pane shell components from docs/specs/ui.md §3, §5, §6.
const (
	shellComponent          = "src/lib/components/Shell.svelte"
	sidebarComponent        = "src/lib/components/Sidebar.svelte"
	topBarComponent         = "src/lib/components/TopBar.svelte"
	listRowComponent        = "src/lib/components/ListRow.svelte"
	collectionListComponent = "src/lib/components/CollectionList.svelte"
	resourceHeaderComponent = "src/lib/components/ResourceHeader.svelte"
	tokensCSS               = "src/lib/tokens.css"
)

var (
	// rawColorPattern matches a colour literal: a hex value or a functional
	// colour other than color-mix().
	rawColorPattern = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|oklch|oklab|lab|lch)\(`)
	// inlineHandlerPattern matches an inline on*="…" event handler, which the
	// strict Content Security Policy forbids.
	inlineHandlerPattern = regexp.MustCompile(`\bon[a-z]+="`)
	// varRefPattern matches a var(--token) reference and captures the name.
	varRefPattern = regexp.MustCompile(`var\((--[a-z0-9-]+)\)`)
	// tokenDeclPattern captures every custom property declared in tokens.css.
	tokenDeclPattern = regexp.MustCompile(`--([a-z0-9-]+):`)
)

// componentStyle returns the component source and the CSS between its <style>
// and </style> tags. It fails when the block is missing, so no test passes on
// an absent style block.
func componentStyle(t *testing.T, path string) (source, style string) {
	t.Helper()

	source = readFile(t, path)

	open := strings.Index(source, "<style>")
	if open < 0 {
		t.Fatalf("%s has no <style> block, want the component CSS", path)
	}
	closeTag := strings.Index(source[open:], "</style>")
	if closeTag < 0 {
		t.Fatalf("%s <style> block has no </style>, want a complete block", path)
	}

	return source, source[open+len("<style>") : open+closeTag]
}

// rule returns the declaration block of selector in style. It fails when the
// selector is absent, so a renamed class can never pass on an empty block.
func rule(t *testing.T, style, selector string) string {
	t.Helper()

	index := strings.Index(style, selector+" {")
	if index < 0 {
		t.Fatalf("no %q rule in the <style> block, want the selector written as %q", selector, selector+" {")
	}

	return braceBlock(t, style[index:], selector)
}

// wantDeclaration asserts that block declares property with value want, and
// quotes the selector so a failure names the rule it came from.
func wantDeclaration(t *testing.T, path, selector, block, property, want string) {
	t.Helper()

	if got := declarationValue(block, property); got != want {
		t.Errorf("%s %s %s = %q, want %q", path, selector, property, got, want)
	}
}

// inOrder asserts that every needle exists in source and that each one starts
// after the previous one ends.
func inOrder(t *testing.T, path, source string, needles ...string) {
	t.Helper()

	offset := 0
	for _, needle := range needles {
		index := strings.Index(source[offset:], needle)
		if index < 0 {
			t.Fatalf("%s: %q is missing after offset %d, want the markup in this order: %q", path, needle, offset, needles)
		}
		offset += index + len(needle)
	}
}

// openingTag returns the opening tag of element in source, so a test can check
// its attributes whatever their order.
func openingTag(t *testing.T, path, source, element string) string {
	t.Helper()

	start := strings.Index(source, "<"+element)
	if start < 0 {
		t.Fatalf("%s has no <%s element, want one", path, element)
	}
	end := strings.IndexByte(source[start:], '>')
	if end < 0 {
		t.Fatalf("%s <%s element has no closing >, want well-formed markup", path, element)
	}

	return source[start : start+end+1]
}

// varToken returns the token name behind a var(--token) value, without the
// leading dashes.
func varToken(t *testing.T, path, value string) string {
	t.Helper()

	match := varRefPattern.FindStringSubmatch(value)
	if match == nil {
		t.Fatalf("%s value = %q, want a var(--token) reference", path, value)
	}

	return strings.TrimPrefix(match[1], "--")
}

// resolveGray resolves token to its gray step in theme and returns that step's
// value, so the frame and canvas lightness come from the tokens rather than a
// restated number.
func resolveGray(t *testing.T, resolved map[string]map[string]oklch, semantic map[string]string, theme, token string) oklch {
	t.Helper()

	alias, ok := semantic[token]
	if !ok {
		t.Fatalf("%s :root has no --%s alias, want it aliased to a gray step", tokensCSS, token)
	}
	step := strings.TrimPrefix(alias, "--")
	value, ok := resolved[theme][step]
	if !ok {
		t.Fatalf("%s theme declares no --%s, want the gray step behind --%s (alias %q)", theme, step, token, alias)
	}

	return value
}

// checkSemantic rejects everything docs/specs/ui.md §5, §11 rules out of a
// component: a colour literal, a ramp step, a shadow or an unsafe construct,
// and any var(--token) that tokens.css does not declare.
func checkSemantic(t *testing.T, path, source string) {
	t.Helper()

	if index := rawColorPattern.FindStringIndex(source); index != nil {
		t.Errorf("%s has the colour literal %q (near %q), want semantic tokens only (docs/specs/ui.md §5)",
			path, source[index[0]:index[1]], snippet(source, index[0]))
	}

	for _, banned := range []struct{ text, reason string }{
		{"--gray-", "want a semantic token such as --surface-frame, never a neutral ramp step"},
		{"box-shadow", "want borders over shadows (docs/specs/ui.md §5)"},
		{"{@html", "want text interpolation, so a hostile label cannot inject markup"},
		{"eval(", "want no eval under the strict Content Security Policy (docs/specs/ui.md §11)"},
		{"new Function", "want no dynamic code under the strict Content Security Policy (docs/specs/ui.md §11)"},
	} {
		if index := strings.Index(source, banned.text); index >= 0 {
			t.Errorf("%s contains %q (near %q), %s", path, banned.text, snippet(source, index), banned.reason)
		}
	}

	if index := inlineHandlerPattern.FindStringIndex(source); index != nil {
		t.Errorf("%s has the inline event handler %q (near %q), want a Svelte handler so the strict Content Security Policy needs no unsafe-inline",
			path, source[index[0]:index[1]], snippet(source, index[0]))
	}

	declared := make(map[string]bool)
	for _, match := range tokenDeclPattern.FindAllStringSubmatch(readFile(t, tokensCSS), -1) {
		declared[match[1]] = true
	}
	for _, match := range varRefPattern.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		if !declared[strings.TrimPrefix(name, "--")] {
			t.Errorf("%s references the undeclared token %s (near %q), want a token from %s",
				path, name, snippet(source, match[0]), tokensCSS)
		}
	}
}

func TestShellTwoPaneLayout(t *testing.T) {
	shell, style := componentStyle(t, shellComponent)

	for _, want := range []string{"import Sidebar", "'./Sidebar.svelte'"} {
		if !strings.Contains(shell, want) {
			t.Errorf("%s is missing %q, want the sidebar mounted in the shell", shellComponent, want)
		}
	}
	for _, want := range []string{"import TopBar", "'./TopBar.svelte'"} {
		if !strings.Contains(shell, want) {
			t.Errorf("%s is missing %q, want the canvas top bar mounted in the shell", shellComponent, want)
		}
	}

	inOrder(t, shellComponent, shell,
		`<div class="shell">`,
		"<Sidebar",
		`<main class="canvas">`,
		"<TopBar",
		"{@render children()}",
		"</main>",
	)

	for _, check := range []struct{ selector, property, want string }{
		{".shell", "display", "grid"},
		{".shell", "grid-template-columns", "var(--sidebar-width) 1fr"},
		{".shell", "background", "var(--surface-frame)"},
	} {
		wantDeclaration(t, shellComponent, check.selector, rule(t, style, check.selector), check.property, check.want)
	}

	checkSemantic(t, shellComponent, shell)

	topBar, topBarStyle := componentStyle(t, topBarComponent)

	inOrder(t, topBarComponent, topBar,
		`<header class="top-bar">`,
		`<nav aria-label="Breadcrumb">`,
		"<ol",
		`aria-current="page"`,
		"{@render actions?.()}",
		"</header>",
	)

	for _, check := range []struct{ selector, property, want string }{
		{".top-bar", "height", "40px"},
		{".top-bar", "box-sizing", "border-box"},
		{".top-bar", "border-bottom", "1px solid var(--border-subtle)"},
	} {
		wantDeclaration(t, topBarComponent, check.selector, rule(t, topBarStyle, check.selector), check.property, check.want)
	}

	checkSemantic(t, topBarComponent, topBar)
}

func TestSidebarStructure(t *testing.T) {
	sidebar, style := componentStyle(t, sidebarComponent)

	for _, want := range []string{"import Orb", "'./Orb.svelte'"} {
		if !strings.Contains(sidebar, want) {
			t.Errorf("%s is missing %q, want the 22 px static orb beside the wordmark", sidebarComponent, want)
		}
	}

	inOrder(t, sidebarComponent, sidebar,
		"<aside",
		"<Orb",
		">Moonglass<",
		"<nav",
		"</aside>",
	)

	for _, element := range []string{"aside", "nav"} {
		if tag := openingTag(t, sidebarComponent, sidebar, element); !strings.Contains(tag, "aria-label=") {
			t.Errorf("%s <%s> = %q, want an aria-label so the landmark is named", sidebarComponent, element, tag)
		}
	}

	const ariaCurrent = `aria-current={item.active ? 'page' : undefined}`
	if !strings.Contains(sidebar, ariaCurrent) {
		t.Errorf("%s is missing %q, want the active nav item marked for assistive technology", sidebarComponent, ariaCurrent)
	}

	for _, check := range []struct{ selector, property, want string }{
		{".wordmark", "font-weight", "600"},
		{".item", "border-radius", "var(--radius-sm)"},
		{".item.active::before", "background", "var(--accent)"},
		{".item:focus-visible", "outline", "var(--focus-ring-width) solid var(--accent)"},
	} {
		wantDeclaration(t, sidebarComponent, check.selector, rule(t, style, check.selector), check.property, check.want)
	}

	checkSemantic(t, sidebarComponent, sidebar)
}

func TestInsetCanvasFrameContrast(t *testing.T) {
	shell, style := componentStyle(t, shellComponent)
	canvas := rule(t, style, ".canvas")

	for _, check := range []struct{ property, want string }{
		{"background", "var(--surface-canvas)"},
		{"border", "1px solid var(--border-subtle)"},
		{"border-radius", "var(--radius-lg)"},
		{"margin", "var(--canvas-margin) var(--canvas-margin) var(--canvas-margin) 0"},
	} {
		wantDeclaration(t, shellComponent, ".canvas", canvas, check.property, check.want)
	}

	css := readFile(t, tokensCSS)
	root := cssBlock(t, css, ":root {")
	if got := declarationValue(root, "--radius-lg"); got != "8px" {
		t.Errorf("%s --radius-lg = %q, want %q so the canvas radius is 8 px (docs/specs/ui.md §3, §5)", tokensCSS, got, "8px")
	}

	if index := strings.Index(shell, "box-shadow"); index >= 0 {
		t.Errorf("%s contains %q (near %q), want borders over shadows (docs/specs/ui.md §5)", shellComponent, "box-shadow", snippet(shell, index))
	}

	semantic := varDecls(root)
	resolved := themes(t)
	frameToken := varToken(t, shellComponent, declarationValue(rule(t, style, ".shell"), "background"))
	canvasToken := varToken(t, shellComponent, declarationValue(canvas, "background"))

	for _, theme := range []string{"dark", "light"} {
		frame := resolveGray(t, resolved, semantic, theme, frameToken)
		inner := resolveGray(t, resolved, semantic, theme, canvasToken)
		if frame.L >= inner.L {
			t.Errorf("%s theme: L(--%s) = %v and L(--%s) = %v, want the frame below the canvas so the canvas reads as raised in both themes (docs/specs/ui.md §5)",
				theme, frameToken, frame.L, canvasToken, inner.L)
		}
	}
}

func TestCollectionListAndRow(t *testing.T) {
	list, listStyle := componentStyle(t, collectionListComponent)

	for _, want := range []string{"import ListRow", "'./ListRow.svelte'"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s is missing %q, want each item rendered as a list row", collectionListComponent, want)
		}
	}

	inOrder(t, collectionListComponent, list,
		`<ul class="list"`,
		`aria-label={label}`,
		"<ListRow",
		"</ul>",
	)

	const bindable = "$bindable(0)"
	if !strings.Contains(list, bindable) {
		t.Errorf("%s is missing %q, want the highlight owned by the parent across navigation", collectionListComponent, bindable)
	}

	for _, want := range []string{"<svelte:window", "onkeydown={handleKey}", "items.length === 0", "Math.min(items.length - 1"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s is missing %q, want keyboard navigation that clamps and never indexes an empty list (docs/specs/ui.md §3, §4)", collectionListComponent, want)
		}
	}

	for _, want := range []string{"'j'", "'k'", "'ArrowDown'", "'ArrowUp'", "'Enter'"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s is missing the key %q, want j/k and the arrow keys to move and Enter to open (docs/specs/ui.md §4)", collectionListComponent, want)
		}
	}
	for _, want := range []string{"isContentEditable", "'INPUT'", "'TEXTAREA'", "metaKey", "ctrlKey"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s is missing the guard %q, want single keys ignored in a text input and under a modifier chord (docs/specs/ui.md §4)", collectionListComponent, want)
		}
	}

	for _, check := range []struct{ property, want string }{
		{"margin", "0"},
		{"padding", "0"},
		{"list-style", "none"},
		{"width", "100%"},
	} {
		wantDeclaration(t, collectionListComponent, ".list", rule(t, listStyle, ".list"), check.property, check.want)
	}
	for _, property := range []string{"border", "border-radius"} {
		if got := declarationValue(rule(t, listStyle, ".list"), property); got != "" {
			t.Errorf("%s .list %s = %q, want no %s so the list runs edge to edge inside the canvas", collectionListComponent, property, got, property)
		}
	}

	row, rowStyle := componentStyle(t, listRowComponent)

	inOrder(t, listRowComponent, row,
		`<li class="row"`,
		`<button type="button"`,
	)

	for _, check := range []struct{ selector, property, want string }{
		{".row", "height", "36px"},
		{".row", "box-sizing", "border-box"},
		{".row", "border-radius", "0"},
		{".row", "border-bottom", "1px solid var(--border-subtle)"},
		{".row:hover", "background", "var(--surface-hover)"},
		{".row.highlighted", "background", "var(--surface-active)"},
	} {
		wantDeclaration(t, listRowComponent, check.selector, rule(t, rowStyle, check.selector), check.property, check.want)
	}

	divider := braceBlock(t, rowStyle, ".row:has(+ .row.highlighted)")
	if got := declarationValue(divider, "border-bottom-color"); got != "transparent" {
		t.Errorf("%s .row:has(+ .row.highlighted) border-bottom-color = %q, want %q so the divider turns transparent and no row changes height (docs/specs/ui.md §6)",
			listRowComponent, got, "transparent")
	}

	checkSemantic(t, collectionListComponent, list)
	checkSemantic(t, listRowComponent, row)
}

func TestResourceHeader(t *testing.T) {
	header, style := componentStyle(t, resourceHeaderComponent)

	inOrder(t, resourceHeaderComponent, header,
		`<header class="header">`,
		`<h1 class="title">{title}</h1>`,
		"{@render status?.()}",
		"{@render actions?.()}",
		"{@render meta?.()}",
	)

	for _, check := range []struct{ selector, property, want string }{
		{".title", "font-size", "var(--text-lg)"},
		{".title", "line-height", "var(--text-lg-leading)"},
		{".title", "letter-spacing", "var(--text-lg-tracking)"},
		{".title", "font-weight", "600"},
		{".header", "padding", "var(--space-8)"},
		{".summary", "color", "var(--text-secondary)"},
	} {
		wantDeclaration(t, resourceHeaderComponent, check.selector, rule(t, style, check.selector), check.property, check.want)
	}

	css := readFile(t, tokensCSS)
	root := cssBlock(t, css, ":root {")
	for _, token := range []struct{ name, want string }{
		{"--text-lg", "22px"},
		{"--text-lg-leading", "28px"},
		{"--text-lg-tracking", "-0.025em"},
	} {
		if got := declarationValue(root, token.name); got != token.want {
			t.Errorf("%s %s = %q, want %q so the resource title resolves to 22/28 at -0.025em (docs/specs/ui.md §5)",
				tokensCSS, token.name, got, token.want)
		}
	}

	checkSemantic(t, resourceHeaderComponent, header)
}
