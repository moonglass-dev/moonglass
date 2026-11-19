---
verdict: ready
reason: ""
---

# Plan for W2-Sa

Context: W2-Sb (scaffold) and W2-Sc (tokens, fonts, Orb, Spinner) are merged on `origin/main` (dfb26d8). This branch was cut at 188df78, before either landed, so `ui/` does not exist here yet. The build step must start from a branch that has `origin/main` merged in (see Risks). This plan targets the `ui/` tree as it is on `origin/main`.

W2-Sa adds the 2-pane shell components from `docs/specs/ui.md` §3, §5, §6: shell, sidebar, canvas top bar, collection list, list row and resource header. It also adds five Go tests that read the component sources, the same way `ui/assets_test.go` and `ui/tokens_test.go` do, so the tests need no Node toolchain or browser. Nothing changes outside `ui/` except `testdata/tests.txt`.

## Files to change

All components are Svelte 5 with runes (`$props`, `$bindable`, `$props.id()`) and `<script lang="ts">`. Styles go in each component's scoped `<style>` block, not in Tailwind classes, because the tests parse CSS. Styles use semantic tokens only: `--surface-*`, `--text-*`, `--border-*`, `--accent*`, plus the scale tokens `--text-*`, `--space-*`, `--radius-*`, `--duration-*`, `--ease-out`, `--focus-ring-*`, `--sidebar-width` and `--canvas-margin`. No hex, `rgb(`, `hsl(`, `oklch(` or `--gray-N`. No `box-shadow` anywhere. No `@keyframes`, so `TestSpinnerReducedMotion` stays green. No `http://` or `https://`, so `TestFontUrlsLocalWOFF2` stays green. No `{@html}`, no `eval`, and no string `on*="…"` attributes. Svelte's `onclick={fn}` compiles to `addEventListener` and is CSP-safe.

- `ui/src/lib/components/Shell.svelte`: the 2-pane shell.
  - Props: `{ nav: NavItem[]; crumbs: Crumb[]; actions?: Snippet; children: Snippet }`. The types are imported from `./Sidebar.svelte` and `./TopBar.svelte`.
  - Markup: `<div class="shell"><Sidebar {nav} /><main class="canvas"><TopBar {crumbs} {actions} />{@render children()}</main></div>`.
  - `.shell`: `position: fixed; inset: 0; display: grid; grid-template-columns: var(--sidebar-width) 1fr; background: var(--surface-frame);`. Fixed positioning avoids a `:global(body)` rule.
  - `.canvas`: `margin: var(--canvas-margin) var(--canvas-margin) var(--canvas-margin) 0; background: var(--surface-canvas); border: 1px solid var(--border-subtle); border-radius: var(--radius-lg); overflow: auto; min-width: 0; color: var(--text-primary); font-family: var(--font-sans);`.
- `ui/src/lib/components/Sidebar.svelte`: the recessed sidebar.
  - Module script: `export type NavItem = { label: string; href: string; active: boolean };`. Props: `{ nav: NavItem[] }`.
  - Markup: `<aside class="sidebar" aria-label="Sidebar">`, containing `<div class="brand"><Orb /><span class="wordmark">Moonglass</span></div>`, then `<nav aria-label="Main"><ul>{#each nav as item (item.href)}<li><a href={item.href} class="item" class:active={item.active} aria-current={item.active ? 'page' : undefined}>{item.label}</a></li>{/each}</ul></nav>`.
  - `.wordmark`: `font-weight: 600; font-size: var(--text-base)`, colour `--text-primary`.
  - `.item`: `--text-secondary`, `border-radius: var(--radius-sm)`, a `min-height` of at least 24 px (§12), and a `background-color` transition at `var(--duration-micro) var(--ease-out)`.
  - `.item:hover`: `background: var(--surface-hover)`.
  - `.item.active`: `background: var(--surface-active); color: var(--text-primary)`. Its `::before` is a 2 px bar with `background: var(--accent)`, which is the active nav mark from §5.
  - `.item:focus-visible`: `outline: var(--focus-ring-width) solid var(--accent); outline-offset: var(--focus-ring-offset)`.
  - Sidebar padding: `var(--space-2)` / `var(--space-3)`.
- `ui/src/lib/components/TopBar.svelte`: the canvas top bar (§3).
  - Module script: `export type Crumb = { label: string; href?: string };`. Props: `{ crumbs: Crumb[]; actions?: Snippet }`.
  - Markup: `<header class="top-bar"><nav aria-label="Breadcrumb"><ol>…</ol></nav><div class="actions">{@render actions?.()}</div></header>`.
  - Every crumb except the last is an `<a href>` when it has an `href`. The last crumb is `<span aria-current="page">`. The `›` separators are `aria-hidden="true"` and coloured `--text-tertiary`.
  - `.top-bar`: `display: flex; align-items: center; justify-content: space-between; height: 40px; box-sizing: border-box; padding: 0 var(--space-4); border-bottom: 1px solid var(--border-subtle);`.
- `ui/src/lib/components/ListRow.svelte`: one 36 px row (§6).
  - Props: `{ id: string; highlighted: boolean; onopen: () => void; children: Snippet }`.
  - Markup: `<li class="row" class:highlighted><button type="button" {id} onclick={onopen}>{@render children()}</button></li>`. The button is the native interactive and focusable element, so no ARIA role or a11y suppression is needed.
  - `.row`: `height: 36px; box-sizing: border-box; border-radius: 0; border-bottom: 1px solid var(--border-subtle); transition: background-color var(--duration-micro) var(--ease-out), border-color var(--duration-micro) var(--ease-out);`.
  - `.row:hover`: `background: var(--surface-hover)`.
  - `.row.highlighted`: `background: var(--surface-active)`.
  - One divider rule: `.row:hover, .row.highlighted, .row:has(+ .row:hover), .row:has(+ .row.highlighted) { border-bottom-color: transparent; }`. The divider turns transparent rather than being removed, so no row changes height.
  - The button fills the row (`width: 100%; height: 100%`, `display: flex`, `gap: var(--space-3)`, `padding: 0 var(--space-4)`) and resets the user-agent styles (`background: none; border: 0; font: inherit; color: inherit; text-align: left`). It uses `--text-base` and `--text-base-leading`.
  - `button:focus-visible`: `outline: var(--focus-ring-width) solid var(--accent); outline-offset: calc(-1 * (var(--focus-ring-width) + var(--focus-ring-offset)))`. The ring is drawn inside the row, so an edge-to-edge row in the scrolling canvas never clips it.
- `ui/src/lib/components/CollectionList.svelte`: the full-width collection list.
  - Generic `<script lang="ts" generics="T">`. Props: `{ label: string; items: T[]; key: (item: T) => string; row: Snippet<[T]>; onopen: (item: T) => void; highlighted?: number }`, where `highlighted = $bindable(0)` lets the parent keep the highlight.
  - `const uid = $props.id()`.
  - Markup: `<ul class="list" aria-label={label}>{#each items as item, i (key(item))}<ListRow id="{uid}-{i}" highlighted={i === highlighted} onopen={() => { highlighted = i; onopen(item); }}>{@render row(item)}</ListRow>{/each}</ul>`, plus `<svelte:window onkeydown={handleKey} />`.
  - `function handleKey(event: KeyboardEvent): void` returns at once in three cases:
    1. Any of `event.metaKey`, `ctrlKey`, `altKey` or `defaultPrevented` is set.
    2. `items.length === 0`.
    3. The target is editable: `target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))`. Single keys do nothing in text inputs (§4).
  - Key handling:
    - `'j'` or `'ArrowDown'` sets `highlighted = Math.min(items.length - 1, highlighted + 1)`.
    - `'k'` or `'ArrowUp'` sets `highlighted = Math.max(0, highlighted - 1)`.
    - Each of these then calls `event.preventDefault()` and `document.getElementById(\`${uid}-${highlighted}\`)?.focus()`, so the highlighted row has keyboard focus, the `:focus-visible` ring and scroll-into-view.
    - `'Enter'` only opens when the event target is not a row button, because a focused button already fires `onopen` on Enter through its click. Then `onopen(items[Math.min(highlighted, items.length - 1)])`.
  - `.list`: `margin: 0; padding: 0; list-style: none; width: 100%;`. The list has no border and no radius, so it runs edge to edge inside the canvas.
- `ui/src/lib/components/ResourceHeader.svelte`: the resource header (§3, §6).
  - Props: `{ title: string; summary?: string; status?: Snippet; meta?: Snippet; actions?: Snippet }`.
  - Markup: `<header class="header"><div class="heading"><h1 class="title">{title}</h1>{@render status?.()}<div class="actions">{@render actions?.()}</div></div>{#if summary}<p class="summary">{summary}</p>{/if}<div class="meta">{@render meta?.()}</div></header>`.
  - `.header`: `padding: var(--space-8)`.
  - `.title`: `margin: 0; font-size: var(--text-lg); line-height: var(--text-lg-leading); letter-spacing: var(--text-lg-tracking); font-weight: 600; color: var(--text-primary);`.
  - `.summary`: `color: var(--text-secondary)`, `--text-base`.
  - `.meta`: `display: flex; flex-wrap: wrap; gap: var(--space-2)`. W2-S fills in the Properties label column.
  - `.actions`: `margin-left: auto`.
- `ui/shell_test.go` (new, `package ui`): the five named tests and three helpers. It reuses `readFile`, `braceBlock`, `declarationValue` and `snippet` from `assets_test.go`.
  - `componentStyle(t, path string) (source, style string)` reads the file and returns the text between `<style>` and `</style>`. It fails when the block is missing.
  - `rule(t, style, selector string) string` finds `selector + " {"` and returns `braceBlock(t, style[i:], selector)`. It fails when the selector is missing, so a renamed class can never pass on an empty block.
  - `checkSemantic(t, path, source string)` makes these checks:
    - `rawColorPattern` (`#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|oklch|oklab|lab|lch)\(`) does not match.
    - There is no `--gray-`, no `box-shadow`, no `{@html`, no `eval(` and no `new Function`.
    - `inlineHandlerPattern` (`\bon[a-z]+="`) does not match.
    - Every `var(--name)` in the file is declared in `src/lib/tokens.css`. The declared set comes from the regex `--([a-z0-9-]+):`.
    - Each failure quotes `snippet(...)` and the expected value.
  - `inOrder(t, path, source string, needles ...string)` asserts that each needle exists and comes after the previous one. It reports the needle and its index when either check fails.
- `testdata/tests.txt`: add the five tests with `make test-list`.

`ui/src/routes/+layout.svelte` does not change. Mounting the shell needs `{org}` routes and nav data, which belong to W2-S. The sign-in route must also stay outside the shell.

## Tests to write

Every test runs table-driven over `{selector, property, want}` declarations where practical, and every failure message shows the expected and actual values.

- `./ui TestShellTwoPaneLayout`
  - Shell imports `Sidebar from './Sidebar.svelte'` and `TopBar from './TopBar.svelte'`.
  - `inOrder` finds `<div class="shell">`, `<Sidebar`, `<main class="canvas">`, `<TopBar`, `{@render children()}` and `</main>`.
  - `.shell` has `display: grid`, `grid-template-columns: var(--sidebar-width) 1fr` and `background: var(--surface-frame)`.
  - TopBar has `<header class="top-bar">`, then `<nav aria-label="Breadcrumb">`, then `<ol`, plus `aria-current="page"` and `{@render actions?.()}`.
  - `.top-bar` has `height: 40px`, `box-sizing: border-box` and `border-bottom: 1px solid var(--border-subtle)`.
  - `checkSemantic` passes on Shell and TopBar.
- `./ui TestSidebarStructure`
  - Sidebar imports `Orb from './Orb.svelte'`.
  - `inOrder` finds `<aside`, `<Orb />`, `>Moonglass<`, `<nav` and `</aside>`. The `<aside` tag carries `aria-label=`, and the `<nav` tag carries `aria-label=`.
  - `aria-current={item.active ? 'page' : undefined}` is present.
  - `.wordmark` has `font-weight: 600`.
  - `.item` has `border-radius: var(--radius-sm)`.
  - `.item.active::before` has `background: var(--accent)`.
  - `.item:focus-visible` has `outline: var(--focus-ring-width) solid var(--accent)`.
  - `checkSemantic` passes on Sidebar.
- `./ui TestInsetCanvasFrameContrast`
  - Shell `.canvas` has `background: var(--surface-canvas)`, `border: 1px solid var(--border-subtle)`, `border-radius: var(--radius-lg)` and `margin: var(--canvas-margin) var(--canvas-margin) var(--canvas-margin) 0`.
  - `--radius-lg` is `8px` in `tokens.css`.
  - The Shell source contains no `box-shadow`.
  - The frame contrast is resolved from the tokens, not restated. The test takes the token named in `.shell`'s and `.canvas`'s `background`, resolves it through `varDecls(cssBlock(t, css, ":root {"))` to its gray step, then reads that step's lightness from `themes(t)`. In both `dark` and `light`, L(frame) must be below L(canvas). The failure message names the theme and both L values.
- `./ui TestCollectionListAndRow`
  - CollectionList imports ListRow, renders `<ul class="list"` with `aria-label={label}`, and contains `<svelte:window onkeydown={handleKey} />` and `$bindable(0)`.
  - The key strings `'j'`, `'k'`, `'ArrowDown'`, `'ArrowUp'` and `'Enter'` are present. The guards `isContentEditable`, `'INPUT'`, `'TEXTAREA'`, `metaKey` and `ctrlKey` are present.
  - `.list` has `margin: 0`, `padding: 0`, `list-style: none` and `width: 100%`, and declares no `border-radius` or `border`.
  - ListRow renders `<li class="row"` and `<button type="button"`.
  - `.row` has `height: 36px`, `box-sizing: border-box`, `border-radius: 0` and `border-bottom: 1px solid var(--border-subtle)`.
  - `.row:hover` has `background: var(--surface-hover)`.
  - `.row.highlighted` has `background: var(--surface-active)`.
  - The style contains `.row:has(+ .row.highlighted)`, and that rule sets `border-bottom-color: transparent`.
  - `checkSemantic` passes on both files.
- `./ui TestResourceHeader`
  - `inOrder` finds `<header class="header">`, `<h1 class="title">{title}</h1>`, `{@render status?.()}`, `{@render actions?.()}` and `{@render meta?.()}`.
  - `.title` has `font-size: var(--text-lg)`, `line-height: var(--text-lg-leading)`, `letter-spacing: var(--text-lg-tracking)` and `font-weight: 600`.
  - `tokens.css` declares `--text-lg: 22px`, `--text-lg-leading: 28px` and `--text-lg-tracking: -0.025em`, so the 22/28/−0.025em spec values are what the tokens resolve to.
  - `.header` has `padding: var(--space-8)`.
  - `.summary` has `color: var(--text-secondary)`.
  - `checkSemantic` passes on ResourceHeader.

Before the components exist, each test fails on `readFile` or on a missing rule (written first, TDD).

## Edge cases

- **Empty collection.** `handleKey` returns before indexing, so `items[…]` is never read out of range. `TestCollectionListAndRow` asserts the `items.length === 0` guard string.
- **Highlight at the ends.** `Math.min` and `Math.max` clamp the index. `Enter` re-clamps against `items.length - 1`, so a shrunk list never opens `undefined`. The test asserts that `Math.min(items.length - 1` is present.
- **Typing in an input.** The editable-target guard means `j`, `k` and `Enter` inside a filter field do nothing (§4). The guard strings are asserted.
- **Modifier chords.** `⌘K`, `Ctrl+…` and `Alt+…` are ignored, so the list never steals the palette or browser shortcuts. `metaKey` and `ctrlKey` are asserted.
- **Double open on Enter.** When a row button has focus, Enter already fires its click. `handleKey` skips `Enter` when the target is a row button (`target.closest('.row')`), so the row opens once.
- **Duplicate IDs.** Row IDs are built from `$props.id()`, so two lists on one page never collide.
- **Hostile labels.** Labels are rendered as text interpolation, never `{@html}`, so names cannot inject markup. `checkSemantic` rejects `{@html` in every shell component.
- **CSP.** There are no inline `on*="…"` attributes, no `eval` and no `new Function`. `checkSemantic` enforces this.
- **A token typo** such as `var(--surface-canvass)` fails the known-token check in `checkSemantic`.
- **Raw colours slipping in.** `checkSemantic` runs on the six new components only. `Orb.svelte` paints its glass with fixed hex stops by design (W2-Sc) and is excluded.

## Risks

- **Stale branch.** `p/W2-Sa` predates W2-Sb and W2-Sc, so `ui/` is missing. The build must first bring the branch up to date with `origin/main` (`scripts/ticket-status set W2-Sa resume`, or the worker's own sync). Without that, every test fails on a missing file. If the build step cannot get `ui/` from `main`, it should write `BLOCKED.md` rather than recreate W2-Sc's files.
- **Diff budget (500 lines).** Aim for about 200 lines of Go test and about 280 of Svelte: Shell ~35, Sidebar ~70, TopBar ~50, ListRow ~45, CollectionList ~55, ResourceHeader ~55. `tests.txt` adds 5 lines. Keep the CSS to the declarations the spec requires: no responsive rail or drawer, no inspector, no pill tabs.
- **Responsive layouts.** The 48 px rail below 1200 px and the drawer below 768 px are in §3, but they are not in this ticket's scope list and need icons and a drawer control. They are left to W2-S, and the review should treat them as out of scope, not as a gap.
- **Style parsing brittleness.** `rule()` matches `selector + " {"` exactly, so each rule has to be written in that form: one selector on its own line, a space, then `{`. The comma-grouped divider rule is checked with a substring test, not `rule()`.
- **Existing tests.** No `@keyframes` (spinner test), no external URLs (fonts test), and nothing in `tokens.css` changes (byte-identical test).
- **`go vet` / lint.** Helpers live only in `shell_test.go`, with no duplicates of the `assets_test.go` names (`readFile`, `braceBlock`, `declarationValue`, `snippet`, `srcFiles`), and the regexes are package-level `MustCompile`. Run `make fmt` and `make ci`.
- **Banned words.** Comments must not use the words "fallback" or "legacy", including in phrases like "system fallback". The words "placeholder" and "slot" are fine.
- **No new dependency.** Only Svelte built-ins and the existing Orb are used, and no Bits UI primitive is needed yet.
