---
id: W2-Sa
title: Design tokens and 2-pane shell layout
status: merged
risk: standard
depends_on: [W2-Sc]
allowed_paths:
  - ui/
max_diff_lines: 500
tests:
  - ./ui TestShellTwoPaneLayout
  - ./ui TestSidebarStructure
  - ./ui TestInsetCanvasFrameContrast
  - ./ui TestCollectionListAndRow
  - ./ui TestResourceHeader
reads:
  - docs/specs/ui.md
  - docs/specs/ui-tokens.css
  - docs/adr/0016-ui-toolchain.md
  - AGENTS.md
---

# Design tokens and 2-pane shell layout

Implement the 2-pane shell layout in `ui/` per `docs/specs/ui.md` §3, §5, §6, §13: recessed sidebar, inset rounded canvas, canvas top bar, collection list, list row, and resource header.

This was split (owner decision, 2026-11-19) into W2-Sb (scaffold), W2-Sc (tokens and static assets), and W2-Sa (2-pane shell layout). W2-Sa depends on W2-Sc and remains the prerequisite for W2-S.

## Scope

- **2-pane shell layout:**
  - Recessed sidebar (`--surface-frame`) on the left and inset working canvas (`--surface-canvas`) on the right.
  - Inset canvas has `--radius-lg` (8 px), 1 px `--border-subtle` hairline border, and no shadow.
  - Recessed chrome around the canvas maintains frame depth in both themes.
- **Sidebar:**
  - Top-left branding: 22 px static orb mark (from W2-Sc) beside the "Moonglass" wordmark (weight 600).
  - Navigation links / sections using semantic tokens and active indicators (`--accent`).
  - Appropriate landmarks (`nav`, `aside`).
- **Canvas top bar:**
  - Breadcrumbs / title area and action button slots within the inset canvas.
- **Collection list and list row:**
  - Full-width list running edge to edge inside the canvas.
  - List rows are square (0 px radius) with 1 px hairline dividers between them (`--border-subtle`).
  - Row states: normal, hover (`--surface-hover`), active/selected (`--surface-active`).
  - Keyboard navigation hook/support for `j`/`k` highlighting and `Enter` opening.
  - Uses only semantic tokens: no raw color literals and no `--gray-N` references.
- **Resource header:**
  - Resource and page titles using `--text-lg` (22/28, weight 600, tracking -0.025em).
  - Status indicator slot, metadata row, and primary action button slot.
- **Go verification tests in `./ui`:**
  - `TestShellTwoPaneLayout`: verifies the DOM/component structure of the 2-pane layout (sidebar and inset canvas).
  - `TestSidebarStructure`: verifies the sidebar components, orb mark placement, wordmark, and accessibility landmarks.
  - `TestInsetCanvasFrameContrast`: verifies the inset canvas styling, border radius (`--radius-lg`), `--border-subtle`, and semantic surface tokens.
  - `TestCollectionListAndRow`: verifies list and row component structure, square edge-to-edge layout, hairline dividers, and semantic token usage.
  - `TestResourceHeader`: verifies typography, `--text-lg` token usage, weight 600, and layout structure.

## Acceptance criteria

- `TestShellTwoPaneLayout` passes.
- `TestSidebarStructure` passes.
- `TestInsetCanvasFrameContrast` passes.
- `TestCollectionListAndRow` passes.
- `TestResourceHeader` passes.
- `go test ./ui` passes.
- Diff stays within the 500-line limit.

## Out of scope

- Registry operations, ⌘K command palette, and specific product routes (handled in W2-S and W4-6).
- SvelteKit scaffold (handled in W2-Sb).
- Token CSS definitions, fonts, and static assets (handled in W2-Sc).

## Security notes

- Strict Content Security Policy compliant: no inline event handlers, no `eval`.
- No sensitive data or secrets rendered in template markup.
- All interactive controls adhere to accessible keyboard navigation and focus models (`docs/specs/ui.md` §4, §12).
