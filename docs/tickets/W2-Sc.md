---
id: W2-Sc
title: Tokens and static assets
status: merged
risk: standard
depends_on: [W2-Sb]
allowed_paths:
  - ui/
max_diff_lines: 450
tests:
  - ./ui TestTokensByteIdenticalToSpec
  - ./ui TestContrastPairsMeetThresholds
  - ./ui TestNeutralRampChromaAndFrame
  - ./ui TestFontUrlsLocalWOFF2
  - ./ui TestOrbStaticAndNoGlobalID
  - ./ui TestSpinnerReducedMotion
reads:
  - docs/adr/0016-ui-toolchain.md
  - docs/specs/ui.md
  - docs/specs/ui-tokens.css
  - AGENTS.md
---

# Tokens and static assets

Add design tokens, self-hosted Geist fonts, the static orb SVG mark, and the spinner arc component in `ui/` per `docs/specs/ui.md` §5, §7, §10, §11 and ADR-0016 (`docs/adr/0016-ui-toolchain.md`). Includes verification Go tests in `./ui`.

This was split out of W2-Sa (owner decision, 2026-11-19) to isolate token contrast and static asset verification.

## Scope

- **Design tokens CSS:** Add `tokens.css` in `ui/src/` (or `ui/src/lib/tokens.css`), matching `docs/specs/ui-tokens.css` byte-for-byte. All tokens use semantic names (`--surface-*`, `--text-*`, `--border-*`, `--accent*`, `--state-*`). The neutral ramp uses OKLCH with chroma <= 0.008 and hue 260. Dark theme is the default in `:root`, with light theme overrides under `[data-theme="light"]` or `@media (prefers-color-scheme: light)`.
- **Self-hosted Geist and Geist Mono fonts:**
  - Place Latin-subsetted variable WOFF2 fonts for Geist and Geist Mono in `ui/static/fonts/` (or `ui/src/lib/fonts/`).
  - Font files total ~65 KB together (no third-party CDN or remote font loading, 0 KB JavaScript).
  - Add `@font-face` declarations with `font-display: swap` and metric-matched system fallbacks (`system-ui` and `ui-monospace`) per `docs/specs/ui.md` §5.
- **Static orb SVG mark:**
  - 22 px dark polished glass orb component placed at top-left of sidebar beside wordmark (`docs/specs/ui.md` §5).
  - 1 px specular rim, quiet teal and blue strands painted inside.
  - Completely static: no animation, no glow, no motion.
  - Paint-server IDs (gradients and filters) must be generated dynamically per instance (e.g. using unique instance IDs); no hardcoded global IDs.
- **Spinner arc component:**
  - Simple spinner arc used for Building (`--state-info`) and Switching (`--state-warn`).
  - The spinner is the only component with `@keyframes` animation; it stops under `prefers-reduced-motion` (`docs/specs/ui.md` §5, §7).
- **Go tests in `./ui`:**
  - `TestTokensByteIdenticalToSpec`: asserts the token definitions in `ui/` are byte-identical to `docs/specs/ui-tokens.css`.
  - `TestContrastPairsMeetThresholds`: computes WCAG 2.2 contrast ratios from the OKLCH values (converting OKLCH -> sRGB with clipping -> relative luminance -> WCAG contrast) across steps 1-4 for all pairs in `docs/specs/ui.md` §5, verifying >= 4.5:1 for text and >= 3:1 for controls/icons.
  - `TestNeutralRampChromaAndFrame`: asserts neutral gray ramp chroma <= 0.008, hue 260, and frame (step 1) is deeper than canvas (step 2) in both dark and light themes.
  - `TestFontUrlsLocalWOFF2`: parses `@font-face` rules to verify that font URLs point only to local WOFF2 files, specify `font-display: swap`, and make zero external network requests.
  - `TestOrbStaticAndNoGlobalID`: verifies that the orb SVG has no animation/keyframe styles and does not use hardcoded static global ID attributes.
  - `TestSpinnerReducedMotion`: verifies that spinner arc CSS includes `@media (prefers-reduced-motion: reduce)` rules that stop the animation.

## Acceptance criteria

- `TestTokensByteIdenticalToSpec` passes.
- `TestContrastPairsMeetThresholds` passes for all required token pairs in both dark and light modes.
- `TestNeutralRampChromaAndFrame` passes.
- `TestFontUrlsLocalWOFF2` passes.
- `TestOrbStaticAndNoGlobalID` passes.
- `TestSpinnerReducedMotion` passes.
- `go test ./ui` passes.
- Diff stays within the 450-line limit (excluding binary font assets).

## Out of scope

- 2-pane shell layout, sidebar navigation, canvas top bar, collection lists, and resource headers (handled in W2-Sa).
- `make ui` build integration and embedding into Go binary (handled in W2-S).

## Security notes

- Invariant: Zero network requests to third-party CDNs or origins at runtime. All fonts and assets are self-hosted and loaded locally.
- Font assets are licensed under SIL Open Font License 1.1 and contain no executable code.
- Dynamic SVG paint-server IDs prevent DOM injection or cross-component ID collisions.
