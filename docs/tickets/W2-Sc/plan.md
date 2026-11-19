---
verdict: ready
reason: ""
---

# Plan for W2-Sc

Nothing here changes a contract, an ADR or a dependency. The fonts are static assets that ADR-0016 decision 5 already approves (Vercel Geist, OFL 1.1, Latin-subset variable WOFF2). They are not added to `package.json`. I computed every contrast pair in `docs/specs/ui.md` §5 from `docs/specs/ui-tokens.css` (OKLab → linear sRGB, clipped, WCAG luminance) and all of them pass. The smallest margins are dark step 8 at 3.35:1 (needs 3) and light `--accent` at 4.99:1 (needs 4.5). The numbers match the spec table, so the contrast test passes on the spec values as they are.

## Files to change

- `ui/src/lib/tokens.css` (new): a byte-for-byte copy of `docs/specs/ui-tokens.css`, made with `cp`. No edits at all, not even a trailing-newline change.
- `ui/static/fonts/geist-latin-wght-normal.woff2` (new, binary, 29 400 bytes, sha256 `19f9c92546aa300c312235e3125af1b81394d8db9a4bc4a425cd5b641d2d54e1`): `package/files/geist-latin-wght-normal.woff2` from `https://registry.npmjs.org/@fontsource-variable/geist/-/geist-5.3.0.tgz`.
- `ui/static/fonts/geist-mono-latin-wght-normal.woff2` (new, binary, 23 128 bytes, sha256 `684ad5b531f81d43c1e8c7038262d5db7cdc1f68006e04d6c7769efa8d33c8cc`): `package/files/geist-mono-latin-wght-normal.woff2` from `https://registry.npmjs.org/@fontsource-variable/geist-mono/-/geist-mono-5.3.0.tgz`.
  - Extract each tarball in a fresh scratch directory and check the sha256 before copying. Do not add either package to `package.json` or `bun.lock`. The two files total about 52 KB, under ADR-0016's figure of about 65 KB.
- `ui/static/fonts/OFL.txt` (new, about 93 lines): `package/LICENSE` from the Geist tarball, copied as it is. Its first line is the Geist Project Authors copyright, which covers both families (the Mono tarball's licence differs only in the `.git` suffix of the URL). OFL 1.1 requires the licence to ship with the fonts, and `static/` is copied into the build, so it does. The text contains none of the banned words (checked).
- `ui/src/lib/fonts.css` (new, about 18 lines): two `@font-face` rules.
  - `font-family: "Geist"`, `src: url("/fonts/geist-latin-wght-normal.woff2") format("woff2")`, `font-weight: 100 900`, `font-style: normal`, `font-display: swap`.
  - The same for `"Geist Mono"` with `/fonts/geist-mono-latin-wght-normal.woff2`.
  - No `local()`, no `@import`, no other origin. The system font stacks (`system-ui`, `ui-monospace`) already sit in `--font-sans` and `--font-mono` in `tokens.css`, which must stay byte-identical. So no extra size-adjusted faces are added: that would need different family names in the stacks, and the spec does not ask for any.
- `ui/src/lib/components/Orb.svelte` (new, about 30 lines). It has no props.
  - `<script lang="ts">` holds `const uid = $props.id();` (Svelte 5 per-instance ID; it gives the same value on the server and in the browser, so hydration matches).
  - A 22×22 `<svg viewBox="0 0 22 22" aria-hidden="true" focusable="false" class="orb">` with no `xmlns` attribute. Svelte does not need one, and leaving it out keeps the no-`http` scan simple.
  - `<defs>`: `radialGradient id="{uid}-glass"` (dark polished glass: highlight near the top-left, from about `#3a4250` to `#10131a`), `linearGradient id="{uid}-rim"` (white from 0.7 to 0.08 opacity, top to bottom), and `clipPath id="{uid}-clip"` (a circle of r 10).
  - The body is a circle `fill="url(#{uid}-glass)"`. Two strands are `<path>`s inside `<g clip-path="url(#{uid}-clip)">` with `fill="none"`, a 1 px stroke and low opacity (0.45–0.55): one teal (`#2fb5b5`-ish, accent hue 195) and one blue (`#5a8fd8`-ish, hue 248).
  - The rim is a circle of r 10.5 with `stroke="url(#{uid}-rim)"` and `stroke-width="1"`.
  - Colours are hex values, because `oklch()` in SVG presentation attributes is not supported everywhere.
  - No `<style>`, `<filter>`, `<animate*>`, `<set>`, `transition` or `animation`.
- `ui/src/lib/components/Spinner.svelte` (new, about 25 lines).
  - Props: `let { kind }: { kind: 'building' | 'switching' } = $props();`.
  - A 14×14 `<svg class="spinner" class:switching={kind === 'switching'} viewBox="0 0 14 14" aria-hidden="true" focusable="false">` holding one circle: `cx=7 cy=7 r=5.5`, `fill=none`, `stroke=currentColor`, `stroke-width=1.5`, round line caps, `stroke-dasharray="22 13"`. That draws an arc of about two thirds of the circle.
  - `<style>`:
    - `.spinner { color: var(--state-info); animation: spin 800ms linear infinite; }`
    - `.switching { color: var(--state-warn); }`
    - `@keyframes spin { to { transform: rotate(360deg); } }`
    - `@media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }`
  - The label comes from the status pill (W2-Sa or later), so the glyph is hidden from assistive technology.
- `ui/src/routes/+layout.svelte`: add `import '$lib/tokens.css';` and `import '$lib/fonts.css';` in the existing script, so both reach the build. Nothing else changes; the shell is W2-Sa's work.
- `ui/tokens_test.go` (new): `TestTokensByteIdenticalToSpec`, `TestContrastPairsMeetThresholds`, `TestNeutralRampChromaAndFrame`, `TestOKLCHContrastConversion`, and helpers. Signatures:
  - `type oklch struct{ L, C, H float64 }`
  - `func cssBlock(t *testing.T, css, openLine string) string`: returns the body between the line equal to `openLine` (for example `:root {`, `  :root:not([data-theme="dark"]) {`, `:root[data-theme="light"] {`) and the next line that ends its block. It calls `t.Fatalf` when the line is missing.
  - `func oklchDecls(t *testing.T, block string) map[string]oklch`: uses the regexp `--([a-z0-9-]+):\s*oklch\(([0-9.]+) ([0-9.]+) ([0-9.]+)\);` and `strconv.ParseFloat`.
  - `func varDecls(block string) map[string]string`: maps `--surface-frame` to `--gray-1`, and so on, from `var(--x)`.
  - `func linearSRGB(c oklch) [3]float64`: the OKLab → LMS → linear sRGB matrices from Björn Ottosson's reference, clipped to [0,1].
  - `func luminance(c oklch) float64`: `0.2126 R + 0.7152 G + 0.0722 B` on the clipped linear channels. This is the same as encoding to sRGB and decoding again with the WCAG formula.
  - `func contrast(a, b oklch) float64`: `(Lhi + 0.05) / (Llo + 0.05)`.
  - `func themes(t *testing.T) map[string]map[string]oklch`: `"dark"` comes from the `:root {` block. `"light"` is dark overlaid with the `:root[data-theme="light"] {` block. It parses `ui/src/lib/tokens.css`.
- `ui/assets_test.go` (new): `TestFontUrlsLocalWOFF2`, `TestOrbStaticAndNoGlobalID`, `TestSpinnerReducedMotion`, plus `func braceBlock(t *testing.T, s, opener string) string` (the text after `opener` up to its matching `}`, counting depth) and `func srcFiles(t *testing.T, exts ...string) map[string]string` (walks `src/` with `filepath.WalkDir`).
- `testdata/tests.txt`: regenerate it with `make test-list`.

Both test files reuse `readFile` from `ui/scaffold_test.go`. They stay in `package ui` and use the standard library only. Run `go test` from `ui/`, so the spec is at `../docs/specs/ui-tokens.css`.

## Tests to write

- `./ui TestTokensByteIdenticalToSpec`: `bytes.Equal(os.ReadFile("src/lib/tokens.css"), os.ReadFile("../docs/specs/ui-tokens.css"))`. On a mismatch it reports the first differing line number, the expected line and the actual line. It also asserts that `src/routes/+layout.svelte` contains `import '$lib/tokens.css';`.
- `./ui TestContrastPairsMeetThresholds`: a table over both themes, checking each token against each of `--gray-1`…`--gray-4`.
  - At 4.5 or more: `gray-11`, `gray-10`, `gray-9`, `accent`, `state-success`, `state-info`, `state-warn`, `state-danger`, `state-rollback`, `state-approval`.
  - At 3 or more: `gray-8`.
  - It also checks `accent-fg` on `accent-solid` at 4.5 or more in each theme.
  - The failure message names the theme, the foreground, the background, the ratio found (two decimals) and the ratio needed.
  - It also asserts that the `prefers-color-scheme: light` block and the explicit `[data-theme="light"]` block declare identical oklch maps. Otherwise the OS-driven light theme would go untested.
  - It asserts that the semantic aliases point at the steps the pairs assume: `--text-primary`→`--gray-11`, `--text-secondary`→10, `--text-tertiary`→9, `--border-control`→8, and `--surface-frame`/`canvas`/`hover`/`active`→1–4.
- `./ui TestNeutralRampChromaAndFrame`: for each of the three blocks (dark `:root`, the media light block, the explicit light block):
  - `--gray-1`…`--gray-11` all exist, with exactly 11 steps.
  - Each has `C <= 0.008` and `H == 260`.
  - `L(gray-1) < L(gray-2)`, in both dark and light (frame deeper than canvas).
  - `--surface-frame` is `var(--gray-1)` and `--surface-canvas` is `var(--gray-2)` in the dark `:root`.
  - Assert no step order beyond frame and canvas: §5 does not specify one.
- `./ui TestFontUrlsLocalWOFF2`:
  - It parses every `@font-face {…}` in `src/lib/fonts.css` and requires exactly two, with families `"Geist"` and `"Geist Mono"`. These are the first entries of `--font-sans` and `--font-mono` in `tokens.css`, and the test asserts those stacks also contain `system-ui` and `ui-monospace`.
  - Each rule has `font-display: swap` and exactly one `url(...)`, with `format("woff2")`. The URL starts with `/fonts/` and not `//`, ends with `.woff2`, and has no scheme. The file `static` + URL exists, its first four bytes are `wOF2`, and it is under 1 MB.
  - `static/fonts/OFL.txt` exists and contains `SIL Open Font License, Version 1.1`.
  - Across every `.css`, `.html`, `.svelte`, `.ts` and `.js` file under `src/`, there is no `http://`, no `https://`, no `url(//` and no `@import` of a non-relative URL.
  - `src/routes/+layout.svelte` imports `$lib/fonts.css`.
- `./ui TestOrbStaticAndNoGlobalID`: reads `src/lib/components/Orb.svelte`.
  - It contains `$props.id()`, and the test captures the variable name with `const (\w+) = \$props\.id\(\)`.
  - Every `id=` attribute (quoted, single-quoted, braced or bare) contains `{<var>}`, and there is at least one id.
  - Every `url(#…)` starts with `#{<var>}`.
  - There is none of `<animate`, `<animateTransform`, `<animateMotion`, `<set`, `@keyframes`, `animation`, `transition`, `<filter` or `feGaussianBlur` (no motion, no glow).
  - The root `<svg` has `width="22"` and `height="22"`.
  - Each failure quotes the bad attribute or token.
- `./ui TestSpinnerReducedMotion`: reads `src/lib/components/Spinner.svelte`.
  - Its `<style>` contains `@keyframes`, an `animation:` declaration with `infinite`, `var(--state-info)` and `var(--state-warn)`.
  - `braceBlock(style, "@media (prefers-reduced-motion: reduce)")` contains `animation: none`.
  - Walking `src/` for `.svelte` and `.css` files, no file other than `lib/components/Spinner.svelte` contains `@keyframes` (spec §7: the spinner is the only loop).
- `./ui TestOKLCHContrastConversion` (helper test): a table of known values.
  - `oklch(1 0 0)` gives luminance 1, and `oklch(0 0 0)` gives 0.
  - `contrast(white, black) == 21`.
  - `oklch(0.627955 0.257683 29.2339)` gives linear sRGB of about (1, 0, 0), within 1e-3.
  - An out-of-gamut `oklch(0.7 0.4 145)` has every channel within [0,1], so clipping works.
  - The reported dark `--text-primary` on `--gray-2` rounds to 16.35 ± 0.05, which pins the method to the spec's table.

Recommendation: before writing any code, write all seven tests and watch them fail.

## Edge cases

- Tokens drift from the spec (whitespace, a missing final newline, CRLF): the byte comparison catches it and reports the line.
- A future token edit breaks a pair, or a light value is edited in only one of the two light blocks: the contrast test checks the thresholds and requires the two light blocks to match.
- An out-of-gamut OKLCH value: the channels are clipped before luminance, and `TestOKLCHContrastConversion` covers it.
- A missing selector or a parse failure: `cssBlock` and `oklchDecls` call `t.Fatalf` and name the selector or line, so the test never passes on an empty map. The ramp test requires all 11 steps.
- A remote font (a CDN URL, a protocol-relative `//`, `data:`), a non-WOFF2 file renamed to `.woff2`, or a missing `font-display`: all are covered by `TestFontUrlsLocalWOFF2`, including the magic-bytes check.
- A hard-coded paint-server ID such as `id="glass"`, or a literal `url(#glass)`: covered by `TestOrbStaticAndNoGlobalID`.
- An animation that creeps into the orb or another component: covered by the orb test and the `@keyframes` walk in `TestSpinnerReducedMotion`.

## Risks

- **Banned words gate:** "fallback" counts in added lines of test files and CSS. Write "system font stack" instead, never "fallback", in comments, test names and messages. Grep the diff for TODO, FIXME, XXX, HACK, fallback and legacy before finishing.
- **Size gate:** the counted lines are about 165 (tokens) + 93 (OFL.txt) + 18 + 30 + 25 + 2, roughly 335, which is under 450. Tests, the binary fonts and `docs/` are not counted. Add no extra components or styling.
- **Clean gate:** if you run `bun run build` to sanity-check the Svelte files, it writes `internal/ui/dist`. The root `.gitignore` does not list that directory, and it is outside `allowed_paths`, so delete it afterwards. `bun run check` (svelte-check) writes only into git-ignored `ui/.svelte-kit`.
- **Network at build time:** the two tarballs come from the npm registry (reachable, checked). If they cannot be fetched or the sha256 does not match, write `BLOCKED.md`. Do not take fonts from anywhere else.
- **Spec text:** `docs/specs/ui.md` §5 says the tokens land "once W2-Sa lands". That sentence is outside `allowed_paths`, and no behaviour contradicts it, so leave it and mention it in the result.
- **Svelte behaviour:** component `<style>` scopes `@keyframes spin` and rewrites the `animation` reference, so the spinner animates. `$props.id()` has to sit at the top level of the instance script.
- **Lint:** use `filepath.WalkDir`, check every error, and use no named returns or globals beyond regexps compiled with `regexp.MustCompile` at package level, matching `scaffold_test.go`. Run `make fmt`, then `make ci`.
