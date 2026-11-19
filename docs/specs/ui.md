# UI foundations

_W0-2 · Revised 2026-11-19 · Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§). Working spec for routes, layout, keyboard model, tokens, components, motion, live-data split and budgets. The owner accepted the routes, keyboard model, live-data split and budgets on 2026-11-16, and on 2026-11-19 adopted the 2-pane inset canvas, the motion cut, contrast-checked tokens and a distinct "Waiting for approval" treatment. Open product choices are in §13._

The stack is fixed by S§3.16: Svelte 5, SvelteKit in static mode, Tailwind and Bits UI, compiled to static files and embedded in the Go binary. The UI is calm, tight, fast and quiet (V§1.2). It borrows Linear's discipline — a recessed sidebar framing an inset rounded canvas, flat hairlines, generous whitespace on resource pages, and keyboard-first flow — while keeping Moonglass's own palette, glass orb mark, typography and deploy workflows.

## 1. Principles

Each principle is a pass/fail check in design and code review.

1. **Keyboard complete.** Every operation in the UI is reachable without a pointer via `⌘K`, `g` chords or single-key list bindings, and every focusable element shows `:focus-visible`.
2. **Instant over spinners.** Cached data renders on frame 1 (≤50 ms route transition); loading skeletons never appear before 150 ms.
3. **Colour means state, never decoration.** One accent hue marks focus, the primary action and active selection; semantic hues appear only on state indicators and diff lines. No decorative gradients, hero banners or card grids.
4. **Numbers over adjectives.** Copy states measurable facts ("0 failed requests across 10 deploys", "42s build"), never marketing words. No war words; always "Moonglass agent" or "coding agent", never bare "agent" (V§1.2).
5. **Preview consequences before confirmation.** Any action that restarts, stops, deletes or requires approval displays the exact diff and the affected services before the confirm step (S§5.3).

## 2. Information architecture

The hierarchy is organisation → projects → environments → services → deploys. Servers, approvals, audit and settings sit beside it at the organisation level.

| Route | Shows |
|---|---|
| `/sign-in` | GitHub and Google on hosted; GitHub and the login-link instruction on self-hosted (ADR-0005, `docs/specs/auth.md`) |
| `/{org}` | Redirects to `/{org}/projects` |
| `/{org}/projects` | Project list |
| `/{org}/projects/{project}` | A project: its environments and services |
| `/{org}/projects/{project}/{env}` | An environment: services, what is live, protected flag, secrets (names only), deleted services (W5-7, `docs/specs/delete.md`) |
| `/{org}/projects/{project}/{env}/{service}` | A service: current release, deploy history, logs, current usage, settings |
| `/{org}/projects/{project}/{env}/{service}/deploys/{deploy}` | One deploy: steps, logs, diff, rollback |
| `/{org}/servers` and `/{org}/servers/{server}` | Servers: online state, key fingerprint, disk, what runs there |
| `/{org}/approvals` and `/{org}/approvals/{approval}` | Pending and past approvals, each with its diff |
| `/{org}/audit` | The audit trail (W5-7, `docs/specs/audit.md`) |
| `/{org}/settings/{section}` | Members, tokens and agent identities, sessions, keys, notifications |

Every `{…}` segment is the resource's opaque, stable ID, never its name. Names are display text, so a link survives a rename. `{org}` is in every route on hosted and self-hosted, so there is one route shape (ADR-0002: self-hosted may hold several organisations).

SvelteKit's static output is one app shell; the server returns it for every UI route and the client routes from there.

## 3. Layout

The shell is two panes: a recessed sidebar and a single inset rounded canvas (8 px radius, 8 px margin on the top, right and bottom, 1 px solid `--border-subtle` border). Resource routes may open a collapsible inspector inside the right edge of that canvas. There is no list-and-detail split.

**Sidebar** (220 px; 48 px icon rail below 1200 px): the 22 px Moonglass orb and the "Moonglass" word mark, navigation, a pending-approval count, the organisation switcher (shown only when the signed-in user belongs to more than one organisation) and a control that opens `⌘K`.

**Route kinds.** Every route in §2 has exactly one kind.

| Kind | Routes | Canvas (≥1200 px) |
|---|---|---|
| Collection | `/{org}/projects`, `/{org}/projects/{project}`, `/{org}/projects/{project}/{env}`, `/{org}/servers`, `/{org}/approvals` | A full-width list inside the inset canvas. No split detail pane and no inspector |
| Resource | `…/{env}/{service}`, `…/deploys/{deploy}`, `/{org}/servers/{server}`, `/{org}/approvals/{approval}` | Main content, with an optional inspector (280 px, 1 px `--border-subtle` left hairline) at the right edge of the canvas |
| Page | `/{org}/audit`, `/{org}/settings/{section}` | One full-width page inside the inset canvas; no inspector |

On a collection route, `j`/`k` highlight a row; `Enter` opens the row as its own resource route, and `Esc` returns to the collection with the row still highlighted. Selection, scroll position and filters survive navigating away and back.

**Widths.**

| Viewport | Collection or page route | Resource route |
|---|---|---|
| **1200 px and up** | Sidebar 220 px, inset canvas `1fr` | Sidebar 220 px, inset canvas as `1fr` main (minimum 600 px) plus a 280 px inspector, open by default, toggled with `⌘I` or `]` |
| **768–1199 px** | Sidebar collapses to a 48 px icon rail; inset canvas `1fr`. No inspector | Rail and inset canvas; the inspector's properties appear in the page Properties block |
| **Under 768 px** | One full-bleed column (the canvas margin and radius drop to 0). The list is the page; opening an item replaces it with the resource and a back control. The sidebar becomes a drawer | One column, the resource with the property strip and a back control; the sidebar is a drawer |

**Canvas top bar.** No hero. 40 px high, with a 1 px bottom hairline:

1. A **breadcrumb** on the left: `Org › Project › Env › Service`, ending at the open resource. Under 768 px the bar shows the back control and the last segment.
2. At most two contextual quick actions on the right, plus the row-actions menu (`.`) and, on a resource route, the inspector toggle. Quick actions come from the same server-supplied list as the palette (§4). A destructive one opens its confirmation (§11).

**Pill tabs** (resource routes only). A 40 px bar under the top bar: `Overview`, `Releases`, `Logs`, `Settings` (or the tabs that resource actually has). The active pill moves in 160 ms (§7).

**Resource header.** Inside the canvas, with 32 px padding: the page title (`--text-lg`, 22 px, weight 600), a one-line summary in `--text-secondary`, then an inline Properties row (a 100 px muted label column and value chips: live status, active commit, target server, environment protection, last deployed by and when).

**Inspector.**

- It holds passive and live information only: live CPU and memory sparklines, the active commit SHA, the environment's protection flags, timestamps, and the target server. It may link to a related resource, such as the server. It has no control that changes state.
- Everything interactive stays in the main canvas: the log stream, the diff view, and the approve and decline buttons.
- Layout is flat: a 1 px left hairline (`--border-subtle`, §5), 20 px padding, label and value pairs in one column. No nested cards or boxes. Labels are `--text-sm` in `--text-tertiary`; values are `--text-base` in `--text-primary`; identifiers are monospace `--text-sm` at 12/18. Groups are separated by 16 px of space, not by rules.
- Each sparkline is shown with its current value as text, and the graphic is hidden from assistive technology (§12).
- **Property strip.** Below 1200 px on a resource route, the same properties appear as a compact horizontal strip between the resource header and the content. It wraps onto a second line rather than scrolling, shows current values as text, and has no sparklines.
- **Toggle.** `⌘I` or `]` (§4). The open or closed state is stored per viewport class in the browser's local storage, as a user preference that is never sent to the server. Classes are 768–1199 px and 1200 px and up, for resource routes; there is no inspector to toggle on a collection or page route, or below 768 px. The state is read before the shell first renders, so toggling and loading never shift layout (§10). On a collection or page route the toggle does nothing, and its palette entry is disabled with that reason.

**Dividers.** The sidebar and the canvas are separated by the 8 px frame margin; the inspector is separated from main content by a 1 px `--border-subtle` line, never by a shadow. Each internal pane draws one such line on its leading edge, except the first, so a boundary is never doubled.

```
Projects (collection)                    ≥1200 px

+------------------+-----------------------------------------------+
| (o) Moonglass    | Org › Projects              Filter    + New   |
|                  |-----------------------------------------------|
|  Projects        |  ●  billing    prod (protected)   3 svc  2m   |
|  Servers         |  ◐  web-app    prod               2 svc  14s  |
|  Approvals    2  |  ×  worker-eu  prod               1 svc  1h   |
|  Audit           |  ○  docs       prod               1 svc  5d   |
|  Settings        |                                               |
|                  |                                               |
|  [⌘K]            |                                               |
+------------------+-----------------------------------------------+
 220 px             inset canvas, 8 px radius and margin
```

```
Service (resource)                       ≥1200 px

+------------------+--------------------------------+--------------+
| (o) Moonglass    | billing › prod › api   Deploy .  [ ]          |
|                  |--------------------------------+--------------+
|  Projects        | (Overview) (Releases) (Logs)   | Telemetry    |
|  Servers         |                                | CPU 14%      |
|  Approvals    2  | api                            | RAM 312M     |
|  Audit           | api.example.dev                | Disk 4.2/40G |
|  Settings        |                                |              |
|                  | Properties  [● Live]  #a8f31c2 | prod, locked |
|                  |             vps-01    2m ago   |              |
|  [⌘K]            | Recent releases                |              |
+------------------+--------------------------------+--------------+
 220 px             main (1fr, min 600 px)            inspector 280 px
```

## 4. Keyboard model

- **⌘K (Ctrl+K) opens the command palette.** Its commands are generated from the operation registry, and it shows only what the signed-in user may run (B W2-S, ADR-0006). The server supplies that list; hiding a command is a convenience, and the registry still enforces every call. The palette also searches projects, services and servers by name. Alongside the registry commands it lists the client-only ones, such as "Toggle inspector"; they come from the shortcut table below, carry no authority and make no registry call.
- **`g` then a letter navigates:** `g p` projects, `g s` servers, `g a` approvals, `g l` audit log, `g o` organisation settings.
- **Single keys inside lists:** `j`/`k` or the arrow keys move, `Enter` opens, `Esc` goes back or clears, `/` filters, `x` selects, `.` opens the row's actions. In a log view, `G` jumps to the bottom and resumes follow.
- **`⌘I` (Ctrl+I) or `]` toggles the inspector** where there is one (§3). `⌘I` works while a text input has focus; `]` is a single key and follows the rule below. The handler cancels the browser's own binding for `⌘I`. The open or closed state is kept per viewport class (§3).
- **`?` opens a sheet of every shortcut,** generated from the same table the bindings use, so it cannot drift. The table includes the inspector toggle, which is also a palette command.
- **Focus is always visible to a keyboard user:** a 2 px solid accent ring with a 2 px offset, drawn on `:focus-visible` and on no other state, on every focusable element, in both themes. Keyboard navigation (`Tab`, `j`/`k`, `⌘K`) shows the ring; a pointer click on a button or row does not. Scroll containers keep 4 px of padding so the ring is never clipped. Focus moves into a dialog when it opens and back to its trigger when it closes.
- Single keys, `]` included, do nothing while a text input has focus, and never run a destructive action directly; they open its confirmation.
- A setting turns single-key shortcuts off, as WCAG 2.2 criterion 2.1.4 requires.

## 5. Design tokens

Components use semantic tokens only (`--surface-*`, `--text-*`, `--border-*`, `--accent`, `--state-*`); the light and dark themes are two sets of values for the same names. Dark is the default in `:root`. The theme follows the operating system until the user chooses. Exact CSS custom properties live in `docs/specs/ui-tokens.css` and, once W2-Sa lands, in the UI build.

**Typefaces:** self-hosted Latin-subsetted WOFF2 variable fonts, Geist and Geist Mono, embedded in the static build (about 65 KB together, 0 KB of JavaScript, no request to any other origin). They are loaded with `font-display: swap` and system fallbacks (`system-ui` and `ui-monospace`) so the first paint does not shift (CLS 0). Inter is excluded. The files count toward the 1 MB embedded-build budget in §10.

**Mark:** a 22 px dark polished glass orb, a static SVG, at the top-left of the sidebar beside the "Moonglass" word mark (weight 600). It has a 1 px specular rim and quiet teal and blue strands painted inside the sphere. It does not glow, and it does not move (§7). Paint-server IDs in the SVG (`id` on a gradient or filter) must be unique per instance, so two orbs on one page do not clash; they must not be fixed global IDs.

**Type scale** (five sizes; size / line height, and tracking where it is not zero):

| Token | Size / line height | Tracking | Weight | Use |
|---|---|---|---|---|
| `--text-xs` | 11/16 | +0.02em | 500 | Section labels, keyboard hints, table headers |
| `--text-sm` | 12/16 (18 mono) | 0 | 400, 500 | Secondary metadata, inspector labels, log lines, SHAs |
| `--text-base` | 13/20 | −0.005em | 400, 500 | Body, list rows, inputs, buttons, inspector values |
| `--text-md` | 15/22 | −0.015em | 500, 600 | Dialog titles, section headings, empty-state titles |
| `--text-lg` | 22/28 | −0.025em | 600 | Resource and page titles |

Weights are 400, 500 and 600. `font-variant-numeric: tabular-nums` applies to timestamps, counts, durations and metrics.

**Spacing:** a 4 px grid: 0, 4, 8, 12, 16, 20, 24, 32, 48.

**Radius:** `--radius-sm` 4 px (buttons, inputs, chips, nav items, `kbd`); `--radius-md` 6 px (popovers, dropdowns, approval cards); `--radius-lg` 8 px (inset canvas, dialogs, the `⌘K` palette); `--radius-full` 9999 px (the orb, pill tabs, status dots, status pills). List rows are square (0 px) and run edge to edge.

**Elevation.** Borders over shadows. The inset canvas, the inspector and list rows use a 1 px `--border-subtle` line and no shadow. Only floating layers (dropdown, popover, dialog, `⌘K`) combine a 1 px `--border-strong` line with `--shadow-overlay`.

**Neutral grey ramp**, lightness in OKLCH, chroma 0.008 or less, hue 260. In both themes, step 1 (`--surface-frame`) is the recessed sidebar and the chrome around the canvas, and step 2 (`--surface-canvas`) is the inset working canvas. The frame is deeper than the canvas in both themes, so the canvas reads as raised without a shadow.

| Step | Role | Dark `oklch(L C H)` | Light `oklch(L C H)` |
|---|---|---|---|
| 1 | Frame / sidebar (`--surface-frame`) | 0.115 0.004 260 | 0.968 0.003 260 |
| 2 | Inset canvas (`--surface-canvas`) | 0.145 0.005 260 | 0.992 0.002 260 |
| 3 | Hover (`--surface-hover`) | 0.185 0.006 260 | 0.945 0.004 260 |
| 4 | Active / selected (`--surface-active`) | 0.220 0.007 260 | 0.915 0.005 260 |
| 5 | Structural hairline (`--border-subtle`) | 0.245 0.006 260 | 0.895 0.005 260 |
| 6 | Strong hairline (`--border-strong`) | 0.315 0.007 260 | 0.820 0.006 260 |
| 7 | Disabled text and icon (`--text-disabled`) | 0.440 0.007 260 | 0.680 0.007 260 |
| 8 | Control borders and muted icons (`--border-control`) | 0.535 0.008 260 | 0.555 0.008 260 |
| 9 | Tertiary text (`--text-tertiary`) | 0.635 0.008 260 | 0.475 0.008 260 |
| 10 | Secondary text (`--text-secondary`) | 0.750 0.007 260 | 0.390 0.008 260 |
| 11 | Primary text (`--text-primary`) | 0.935 0.004 260 | 0.200 0.006 260 |

Text uses steps 9–11 only, and only on steps 1–4; step 5 is never a text background.

**Borders.** All 1 px solid:

- `--border-subtle` (step 5): the structural hairline of the canvas, the inspector and list rows (§3, §6). No shadow divides panes or rows.
- `--border-strong` (step 6): the edge of a floating layer.
- `--border-control` (step 8): the edge of an input, an unfilled button, a checkbox or a switch, and muted icons.

**Accent and semantic hues**, muted enough to sit in a dense list. Pill and banner tints use `color-mix(in oklch, var(--state-*) 10%, transparent)`.

| Token | Role | Dark `oklch` | Light `oklch` |
|---|---|---|---|
| `--accent` | Focus ring, links, the active nav mark | 0.75 0.095 195 | 0.47 0.090 195 |
| `--accent-solid` | Primary button fill (label `--accent-fg`) | 0.50 0.095 195 | 0.47 0.090 195 |
| `--accent-fg` | Label on `--accent-solid` | 0.985 0.002 260 | 0.985 0.002 260 |
| `--state-success` | Live, online, diff add | 0.72 0.110 152 | 0.46 0.100 152 |
| `--state-info` | Building | 0.70 0.110 248 | 0.47 0.120 248 |
| `--state-warn` | Switching | 0.77 0.110 80 | 0.47 0.100 75 |
| `--state-danger` | Failed, offline, diff remove | 0.68 0.140 25 | 0.48 0.150 25 |
| `--state-rollback` | Rolled back, shadow rollback | 0.72 0.120 52 | 0.48 0.120 52 |
| `--state-approval` | Waiting for approval | 0.72 0.110 300 | 0.47 0.110 300 |

The accent stays hue 195. If a later token test fails 4.5:1, it moves only lightness and chroma. Blue would sit too close to Building, amber to Switching, and indigo is Linear's. Waiting for approval has its own hue (300) so it cannot be confused with Switching.

**Contrast.** Ratios below were computed by converting each OKLCH value to sRGB (clipping any channel that sits outside the gamut), then to relative luminance, then to the WCAG 2.2 contrast ratio. They are reported as the worst pair against steps 1–4, then against the canvas (step 2). These L values already meet the thresholds, so none were moved.

| Pair | Dark (worst → canvas) | Light (worst → canvas) | Needs |
|---|---|---|---|
| `--text-primary` (step 11) | 14.3:1 → 16.4:1 | 14.1:1 → 17.7:1 | 4.5 |
| `--text-secondary` (step 10) | 7.8:1 → 8.9:1 | 7.5:1 → 9.4:1 | 4.5 |
| `--text-tertiary` (step 9) | 5.0:1 → 5.8:1 | 5.2:1 → 6.5:1 | 4.5 |
| `--border-control` / muted icons (step 8) | 3.4:1 → 3.8:1 | 3.7:1 → 4.6:1 | 3 |
| `--accent` text and focus ring | 8.1:1 → 9.3:1 | 5.0:1 → 6.3:1 | 4.5 text, 3 focus |
| `--accent-fg` on `--accent-solid` | 5.4:1 | 6.2:1 | 4.5 |
| `--state-success` text and glyph | 7.3:1 → 8.4:1 | 5.3:1 → 6.6:1 | 4.5 |
| `--state-info` text and glyph | 6.5:1 → 7.5:1 | 5.3:1 → 6.6:1 | 4.5 |
| `--state-warn` text and glyph | 8.3:1 → 9.5:1 | 5.4:1 → 6.8:1 | 4.5 |
| `--state-danger` text and glyph | 5.6:1 → 6.5:1 | 5.5:1 → 6.9:1 | 4.5 |
| `--state-rollback` text and glyph | 6.7:1 → 7.7:1 | 5.3:1 → 6.7:1 | 4.5 |
| `--state-approval` text and glyph | 6.7:1 → 7.7:1 | 5.6:1 → 7.0:1 | 4.5 |
| `--border-subtle` (step 5) | 1.1:1 | 1.1:1 | None: see below |

Step 7 (`--text-disabled`) is for disabled states only and is not required to meet 4.5:1. `--border-subtle` is below 3:1 by design. It divides regions and does not identify a control or its state, so WCAG 1.4.11 does not require 3:1 of it, and every region is also identifiable by its landmark and its place in the tab order. A control that is bordered uses `--border-control`. The token test (§12) checks every pair above and fails the build if a value moves a pair under its threshold.

**Deploy states.** Each state has a token, a 14 px glyph and a label, so it never depends on colour alone.

| State | Token | Glyph | Motion | List treatment |
|---|---|---|---|---|
| Queued | `--text-tertiary` | Hollow circle | None | Glyph and muted label |
| Building | `--state-info` | Spinner arc | Simple spinner arc only (§7) | Glyph and elapsed seconds |
| Switching | `--state-warn` | Spinner arc | Simple spinner arc only (§7) | Glyph and "Switching" |
| Live | `--state-success` | Filled dot | None | Glyph only (the quietest state) |
| Failed | `--state-danger` | Cross | None | Glyph and exit or step reason |
| Rolled back | `--state-rollback` | Return arrow | None | Glyph and target SHA |
| Waiting for approval | `--state-approval` | Pause bars | None | Glyph and "Approval" |
| Would have rolled back | `--state-rollback` | Dashed return arrow (outline) | None | Glyph and the shadow report link (S§5.4) |
| Superseded / stopped | `--text-tertiary` | Hollow circle | None | Glyph and muted text |

"Would have rolled back" is the shadow-mode report (S§5.4); it shares the rolled-back hue in outline so it reads as a rollback that did not happen. Waiting for approval must not share Switching's hue or glyph.

## 6. Components

**Bits UI primitives used:** Command (the palette), Dialog, Alert Dialog, Dropdown Menu, Popover, Tooltip, Select, Tabs, Switch, Checkbox, Label, Separator. A primitive is added to this list before it is used.

**Composite components:**

| Component | What it is |
|---|---|
| List row | One line: state glyph, name, one secondary fact, relative time. 36 px high in every list, with no density setting; fully keyboard driven. Rows are square and edge to edge, divided by a 1 px `--border-subtle` bottom edge that is inside the 36 px and turns transparent, rather than disappearing, on the highlighted, hovered and selected row and the row above, so no row changes height |
| Canvas top bar | A 40 px breadcrumb bar with quick actions on the right. §3 |
| Resource header | Title, one-line summary and the Properties row, with 32 px padding. §3 |
| Pill tabs | Horizontal pills for the resource's views. The active pill moves in 160 ms. §3, §7 |
| Inspector | Read-only column of properties and live telemetry, with a 1 px `--border-subtle` left edge, 20 px padding and no nested boxes, inside the canvas. Below 1200 px it is the property strip. §3 |
| Sparkline | A small line of recent CPU or memory use inside the inspector, always beside its current value as text. It updates with the usage stream (§9) and never animates on its own |
| Status pill | State glyph plus label, in the state token. The only place state colours appear |
| Diff view | What a deploy or approval will change: image, config, environment variable names, services that will restart. Secret values never appear |
| Log stream | Live, monospace, virtualised; pauses following when the reader scrolls up |
| Command palette | §4 |
| Approval card | Who or what asked, the diff, the environment, and Approve or Decline. Approve starts step-up (ADR-0005) |
| Secret input | Write-only field. After saving it shows only that a value is set and when (S§3.11) |
| Typed confirmation | An Alert Dialog whose action is enabled once the resource's name has been typed |

The log stream, diff view, approval card and every other control live in the main canvas, never in the inspector.

Every interactive component has default, hover, active, focus, disabled and loading states.

**Surfaces the plan requires:**

- Before confirming a deploy, the UI flags stop-then-start shapes and lists the Compose services that will restart (S§5.3). In the MVP Compose import runs only from the CLI, whose list before confirmation is that surface (`docs/specs/compose.md`, "The list"); the UI has no Compose screen.
- A shadow-mode report shows the numbers and lets the user mark the call right or wrong (S§5.4).
- A sanitizer rejection names the field and shows the `agent.toml` lines that would allow it (S§3.2).
- After a server is removed, the UI lists the values to rotate at their source (S§3.11).
- Once a quarter the UI asks for a recovery-key check, and it states the cost of losing the key in plain words (S§3.11).
- A new server's enrolment into an unprotected environment is confirmed with step-up (S§3.11).
- Creating an environment shows the `moonglass` CLI command that creates it; the UI cannot make its keys (ADR-0003). An org's first environment shows as protected from the start (ADR-0006).

## 7. Motion

Motion is short and rare. It shows a change of state or position and nothing else.

- **State transitions** use `--duration-micro` (90 ms) for colour, opacity and hairline changes, and `--duration-move` (160 ms) for the active pill, the inspector opening or closing, and other small moves. The easing is `--ease-out` (`cubic-bezier(0.2, 0, 0, 1)`), with no overshoot.
- **Building and Switching** may use a simple spinner arc, and only that. No orbital path, no opposing-arc glide, and no other loop.
- **Nothing else loops, ripples or sweeps.** The orb is a static SVG mark: no strand drift and no acceleration while a deploy is in flight. Live has no settle ripple. The header and tab-bar hairline has no luminous progress sweep.
- With `prefers-reduced-motion`, transitions are an immediate change and the spinner arc does not animate.

## 8. States

- **Empty:** says what belongs here and offers the next action as a button and its shortcut. An empty server list offers the install command.
- **Loading:** cached data appears at once. A skeleton appears only if nothing is cached and the wait passes 150 ms, and it has the final layout's dimensions.
- **Error:** says what happened and what to do, in that order, using the server's error text (`AGENTS.md`: fail loudly, say what to do). It never shows a bare code.
- **Offline or stream lost:** a quiet banner; data on screen is marked with the time it was last current.

## 9. Live data

Live updates arrive over server-sent events (S§3.16, B W4-4).

| Live | Fetched on navigation |
|---|---|
| Deploy state and step progress | The audit trail |
| The log stream | Settings, members, tokens, sessions |
| Server online and offline | Secret names |
| Current resource usage | Deploy history older than the current deploy |
| Pending approvals and their count | |
| Rollback and shadow-mode events | |

Streams are authorised like any other read. When a stream reconnects, the UI fetches the current state again and does not assume it missed nothing.

## 10. Performance budgets

W2-S measures the two size budgets in CI against the built output (ADR-0016). The route-change and layout-shift budgets need a browser; until a ticket adds one under its own ADR, they are checked in the weekly demo.

| Budget | Value |
|---|---|
| Initial JavaScript, gzipped, for the first authenticated screen | 90 KB or less |
| Route change with cached data, input to paint | 50 ms or less |
| Layout shift | None: cumulative layout shift of 0 |
| The static build embedded in the binary, compressed | 1 MB or less |

The age library for browser-side encryption loads only on screens that save a secret (B W4-5). Self-hosted font files count toward the 1 MB budget and must not add JavaScript or a network request to another origin.

## 11. Security

- **A strict Content Security Policy,** sent by the Go server as a header: neither `unsafe-inline` nor `unsafe-eval`. SvelteKit's one start-up script is allowed by its hash; no other inline script exists (B W2-S, B W4-5).
- **No third-party scripts or fonts at run time.** Geist and Geist Mono are self-hosted WOFF2 files in the static build and make no request to any other origin.
- **Secret inputs are write-only** and never echoed back, logged or kept in client state after the save (S§3.11).
- **Destructive actions need a typed confirmation or an approval,** never a single click or a single key.
- Sessions use `__Host-` cookies; the UI stores no token where page script can read it.
- The UI states the limit of browser-side encryption wherever a secret is entered: it trusts the JavaScript we serve, and the CLI is the stronger path (S§3.11, `docs/threat-model.md` §5).

## 12. Accessibility

WCAG 2.2 AA. Text contrast of at least 4.5:1 in both themes, and non-text contrast of at least 3:1 for control boundaries, icons, state shapes and the focus ring, checked by a test over the token values that covers every pair in §5. Structural pane and row hairlines are exempt, for the reason given there. Everything is reachable and operable by keyboard, with a focus indicator on `:focus-visible` (§4). State is never conveyed by colour alone. Live regions announce deploy state changes; the inspector is a labelled `aside` landmark and is not a live region, so telemetry updates are not announced. Targets are at least 24 by 24 px.

## 13. Open choices

The owner accepted the 2026-11-16 defaults below where they still apply, and on 2026-11-19 adopted the 2-pane inset canvas, the recessed frame in both themes, self-hosted Geist, a static orb, the motion cut, contrast-checked token values and a distinct "Waiting for approval" glyph and hue. Each item that is still open stays listed because the first screens may show it should change.

1. **Highlight versus open.** Decided 2026-11-19: on a collection route, `j`/`k` highlight a row in the full-width list; `Enter` opens the resource route. There is no side detail pane that follows the highlight.
2. **Route kinds.** Only deploys and approvals were first named as deep routes. The service and server routes are classed as resource routes too, and the project and environment routes as collections, so every service and server has an inspector. Audit and settings are pages with none. That grouping still holds under the 2-pane shell.
3. **Inspector default.** Decided 2026-11-19: there is no inspector on a collection route. On a resource route at 1200 px and up it is open by default, and the choice is stored per viewport class. Opening it closed by default is the other reading.
4. **Toggle keys.** `⌘I` or `]`, both kept. Some browsers bind `⌘I` or Ctrl+I to their own features, so the handler cancels the default. `]` needs AltGr on some keyboard layouts, as `/` and `.` already may elsewhere.
5. **Hairlines are faint.** At about 1.1:1 the pane dividers are a quiet cue, not a conformance claim. Region identity rests on landmarks, and both themes keep the frame deeper than the canvas.
6. **Row divider lines.** Rows are square and edge to edge, with a hairline between them. Hiding the line beside the highlight is a rule to try in the first screens; if it looks fussy, drop the row dividers and keep the pane ones.
7. **Property strip.** It drops the sparklines; current values stay as text. A compact sparkline in the strip is possible if the first screens show it is wanted.
8. **Token roles and contrast.** Contrast against steps 1–4 was recomputed from the OKLCH values on 2026-11-19 (method in §5). No L value had to move. Icons and control borders stay on step 8, which clears 3:1; disabled stays on step 7. Accent text may sit on steps 1–4.
