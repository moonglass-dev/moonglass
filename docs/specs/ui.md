# UI foundations

_W0-2 · 2026-11-16 · Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§). The owner accepted the routes, layout, keyboard model, tokens, components, live-data split and budgets below as the working spec on 2026-11-16, including the revised layout, keyboard model and tokens and the defaults chosen in §13. The choices still open are listed at the end._

The stack is fixed by S§3.16: Svelte 5, SvelteKit in static mode, Tailwind and Bits UI, compiled to static files and embedded in the binary. The UI should feel calm, elegant, premium and soft (V§1.2), and serve the owner's priorities: speed, performance, UX and security. It is inspired by Linear's restraint and keyboard flow. Its layout details, icons, colours and typography are our own.

## 1. Principles

1. **Keyboard first.** Every action is reachable without a pointer, and the fastest path is a key.
2. **Nothing waits on a spinner that could be instant.** Show cached data at once, then update it.
3. **One accent colour.** Colour otherwise means state, and only state.
4. **Say what is true, with numbers.** "Zero failed requests across ten deploys", never "blazing-fast" (V§1.2).
5. **Tell people before, not after.** Anything that will restart, stop or need approval is shown before the confirm (S§5.3).
6. **Quiet by default.** No decoration that carries no information; motion only to show a change.

Copy follows V§1.2: no war words, and always "Moonglass agent" or "coding agent", never bare "agent".

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

Four regions: **sidebar** (the "Moonglass" word mark, navigation, pending-approval count, and the organisation switcher, shown only when the signed-in user belongs to more than one organisation), **list** (the collection for the current route), **detail** (the highlighted or open item) and **inspector** (a read-only column of properties and live telemetry for that item). Which regions show depends on the route's kind and on the viewport.

**Route kinds.** Every route in §2 has exactly one kind.

| Kind | Routes | Regions |
|---|---|---|
| Collection | `/{org}/projects`, `/{org}/projects/{project}`, `/{org}/projects/{project}/{env}`, `/{org}/servers`, `/{org}/approvals` | Sidebar, list, detail |
| Resource | `…/{env}/{service}`, `…/deploys/{deploy}`, `/{org}/servers/{server}`, `/{org}/approvals/{approval}` | Sidebar, detail, inspector. No list, so logs, diffs and approval controls get the full width |
| Page | `/{org}/audit`, `/{org}/settings/{section}` | Sidebar and one full-width page; no inspector |

On a collection route, `j`/`k` highlight a row and the detail follows it; `Enter` opens the row as its own resource route, and `Esc` returns to the collection with the row still highlighted.

**Widths.**

| Viewport | Collection route | Resource route |
|---|---|---|
| **1440 px and up** | Sidebar 232 px, list 320–440 px, detail the rest. The inspector (260–280 px) docks as a fourth column when toggled open, and is closed by default. While docked, the list is 320 px so the detail keeps at least 600 px | Sidebar 232 px, detail `1fr` with a minimum of 600 px, inspector 260–280 px, open by default |
| **1200–1439 px** | Sidebar 232 px, list 320–440 px, detail the rest. No inspector, so the columns never cramp | As above |
| **768–1199 px** | Sidebar collapses to a 48 px icon rail; list and detail side by side. No inspector | Rail and detail; the inspector's properties appear as the property strip |
| **Under 768 px** | One column. The list is the page; opening an item replaces it with the detail and a back control. The sidebar becomes a drawer | One column, detail with the property strip and a back control; the sidebar is a drawer |

**Detail header.** No hero. Two lines, in this order:

1. A **breadcrumb bar**, 40 px high: `Org > Project > Env > Service` on the left, ending at the open resource, and the contextual quick actions on the right. Quick actions come from the same server-supplied list as the palette (§4): at most two are shown, the rest are in the row-actions menu (`.`), and a destructive one opens its confirmation (§11). Under 768 px the bar shows the back control and the last segment.
2. A **property row**, 32 px high, on one line: the three facts the header always answers first, namely what is live, since when, and who or what deployed it.

**Inspector.**

- It holds passive and live information only: live CPU and memory sparklines, the active commit SHA, the environment's protection flags, timestamps, and the target server. It may link to a related resource, such as the server. It has no control that changes state.
- Everything interactive stays in the detail pane: the log stream, the diff view, and the approve and decline buttons.
- Layout is flat: a 1 px left hairline (`border-subtle`, §5), 16–20 px padding, label and value pairs in one column. No nested cards or boxes. Labels are 12/16 in step 11; values are 13/20 in step 12; identifiers are monospace 12/18. Groups are separated by 16 px of space, not by rules.
- Each sparkline is shown with its current value as text, and the graphic is hidden from assistive technology (§12).
- **Property strip.** Below 1200 px on a resource route, the same properties appear as a compact horizontal strip between the detail header and the content. It wraps onto a second line rather than scrolling, shows current values as text, and has no sparklines.
- **Toggle.** `⌘I` or `]` (§4). The open or closed state is stored per viewport class in the browser's local storage, as a user preference that is never sent to the server. Classes are 1200–1439 px and 1440 px and up, for resource routes and collection routes alike; there is no inspector to toggle below 1200 px. The state is read before the shell first renders, so toggling and loading never shift layout (§10). On a collection route below 1440 px the toggle does nothing, and its palette entry is disabled with that reason.

**Dividers.** Panes are separated by 1 px solid `border-subtle` lines (§5) and never by a shadow. Each pane draws one such line on its leading edge, except the first, so a boundary is never doubled.

Selection, scroll position and filters survive navigating away and back.

## 4. Keyboard model

- **⌘K (Ctrl+K) opens the command palette.** Its commands are generated from the operation registry, and it shows only what the signed-in user may run (B W2-S, ADR-0006). The server supplies that list; hiding a command is a convenience, and the registry still enforces every call. The palette also searches projects, services and servers by name. Alongside the registry commands it lists the client-only ones, such as "Toggle inspector"; they come from the shortcut table below, carry no authority and make no registry call.
- **`g` then a letter navigates:** `g p` projects, `g s` servers, `g a` approvals, `g l` audit log, `g o` organisation settings.
- **Single keys inside lists:** `j`/`k` or the arrow keys move, `Enter` opens, `Esc` goes back or clears, `/` filters, `x` selects, `.` opens the row's actions.
- **`⌘I` (Ctrl+I) or `]` toggles the inspector** where there is one (§3). `⌘I` works while a text input has focus; `]` is a single key and follows the rule below. The handler cancels the browser's own binding for `⌘I`. The open or closed state is kept per viewport class (§3).
- **`?` opens a sheet of every shortcut,** generated from the same table the bindings use, so it cannot drift. The table includes the inspector toggle, which is also a palette command.
- **Focus is always visible to a keyboard user:** a 2 px solid accent ring with a 2 px offset, drawn on `:focus-visible` and on no other state, on every focusable element, in both themes. Keyboard navigation (`Tab`, `j`/`k`, `⌘K`) shows the ring; a pointer click on a button or row does not. Scroll containers keep 4 px of padding so the ring is never clipped. Focus moves into a dialog when it opens and back to its trigger when it closes.
- Single keys, `]` included, do nothing while a text input has focus, and never run a destructive action directly; they open its confirmation.
- A setting turns single-key shortcuts off, as WCAG 2.2 criterion 2.1.4 requires.

## 5. Design tokens

Components use semantic tokens only (`surface`, `text`, `border`, `accent`, `state-*`); the light and dark themes are two sets of values for the same names. The theme follows the operating system until the user chooses.

**Typefaces:** the system UI stack, `system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", sans-serif`, and the system monospace stack, `ui-monospace, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace`. No font files ship, so the embedded build spends nothing on fonts. Inter is excluded.

**Mark:** the word "Moonglass", in the UI face at weight 600, until the owner supplies a one-colour mark that works at 16 px.

**Type scale** (size / line height, px): 11/16, 12/16, **13/20 (base)**, 14/20, 16/24, 20/28, 24/32. Weights 400, 500 and 600. A monospace face at 12/18 for logs, digests, fingerprints and identifiers.

**Spacing:** a 4 px grid: 0, 4, 8, 12, 16, 24, 32, 48, 64.

**Radius:** 4 px (inputs, pills), 8 px (buttons, rows, cards), 12 px (dialogs, palette).

**Neutral grey ramp**, lightness in OKLCH, chroma 0.01 or less, with a slight cool tint:

| Step | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Light | 0.99 | 0.975 | 0.955 | 0.93 | 0.90 | 0.86 | 0.80 | 0.70 | 0.56 | 0.48 | 0.40 | 0.20 |
| Dark | 0.135 | 0.165 | 0.195 | 0.225 | 0.26 | 0.32 | 0.38 | 0.47 | 0.56 | 0.63 | 0.74 | 0.94 |

Steps 1–2 are surfaces: step 1 is the app frame (the sidebar and the chrome around the panes) and step 2 is the canvas (list, detail and inspector). In dark, the frame is deeper than the canvas, so the canvas reads as raised without a shadow. Steps 3–4 are hover and selected, 5–7 borders, 8 disabled, 9 icons and control borders, 10–12 text. Text uses steps 10–12 only, and only on steps 1–4; step 5 is never a text background.

**Borders.** Two border tokens, both 1 px solid:

- `border-subtle` (step 5): the structural hairline between the sidebar, list, detail and inspector panes (§3), and between list rows (§6). No shadow divides panes or rows.
- `border-control` (step 9): the edge of an input, an unfilled button, a checkbox or a switch.

**Contrast.** Approximate, computed from these lightness values at chroma 0.01 against steps 1–4 of the same theme; the token test (§12) uses the final values:

| Pair | Light | Dark | Needs |
|---|---|---|---|
| Step 10 text | 5.3–6.3 | 4.9–5.7 | 4.5 |
| Step 9 (icons, `border-control`) | 3.8–4.5 | 3.7–4.3 | 3 |
| Accent text, steps 1–3 | 4.6–5.1 | 8.9–9.7 | 4.5 |
| Accent focus ring, steps 1–4 | 4.3–5.1 | 8.3–9.7 | 3 |
| `border-subtle` (step 5) | 1.1–1.3 | 1.1–1.3 | None: see below |

Step 11 and 12 text is higher still. Step 8 reaches at most 2.9, so it carries disabled states and nothing that must be seen. Light accent text on step 4 is 4.28, so accent text sits on steps 1–3.

`border-subtle` is below 3:1 by design. It divides regions and does not identify a control or its state, so WCAG 1.4.11 does not require 3:1 of it, and every region is also identifiable by its landmark and its place in the tab order. A control that is bordered uses `border-control`. The token test (§12) checks every pair above and fails the build if a value moves a pair under its threshold.

**Accent:** one hue, used for the focus ring, the primary button, the selected navigation item and links. A muted sea-glass teal, `oklch(0.52 0.09 195)` on light and `oklch(0.76 0.10 195)` on dark, dark enough on light to carry link text and a white button label. The hue stays 195; if the contrast test fails 4.5:1 at W2-S, it moves only the lightness and chroma. Blue would sit too close to Building, amber to Waiting for approval, and indigo is Linear's.

**Deploy states.** Each state has a hue, an icon shape and a label, so it never depends on colour alone.

| State | Hue | Shape |
|---|---|---|
| Building | Blue (250) | Partial ring |
| Live | Green (150) | Filled dot |
| Failed | Red (25) | Cross |
| Rolled back | Orange (55) | Return arrow |
| Waiting for approval | Amber (85) | Pause |
| Would have rolled back | Orange (55), outline only | Dashed return arrow |
| Queued, superseded, stopped | Grey | Hollow dot |

"Would have rolled back" is the shadow-mode report (S§5.4); it shares the rolled-back hue in outline so it reads as a rollback that did not happen.

## 6. Components

**Bits UI primitives used:** Command (the palette), Dialog, Alert Dialog, Dropdown Menu, Popover, Tooltip, Select, Tabs, Switch, Checkbox, Label, Separator. A primitive is added to this list before it is used.

**Composite components:**

| Component | What it is |
|---|---|
| List row | One line: state shape, name, one secondary fact, relative time. 36 px high in every list, with no density setting; fully keyboard driven. Rows are divided by a 1 px `border-subtle` bottom edge that is inside the 36 px and turns transparent, rather than disappearing, on the highlighted, hovered and selected row and the row above, so no row changes height |
| Detail header | A 40 px breadcrumb bar with quick actions on the right, then a 32 px property row: what is live, since when, who or what deployed it. §3 |
| Inspector | Read-only column of properties and live telemetry, with a 1 px `border-subtle` left edge, 16–20 px padding and no nested boxes. Below 1200 px it is the property strip. §3 |
| Sparkline | A small line of recent CPU or memory use inside the inspector, always beside its current value as text. It updates with the usage stream (§9) and never animates on its own |
| Status pill | State shape plus label, in the state hue. The only place state colours appear |
| Diff view | What a deploy or approval will change: image, config, environment variable names, services that will restart. Secret values never appear |
| Log stream | Live, monospace, virtualised; pauses following when the reader scrolls up |
| Command palette | §4 |
| Approval card | Who or what asked, the diff, the environment, and Approve or Decline. Approve starts step-up (ADR-0005) |
| Secret input | Write-only field. After saving it shows only that a value is set and when (S§3.11) |
| Typed confirmation | An Alert Dialog whose action is enabled once the resource's name has been typed |

The log stream, diff view, approval card and every other control live in the detail pane, never in the inspector.

**Surfaces the plan requires:**

- Before confirming a deploy, the UI flags stop-then-start shapes and lists the Compose services that will restart (S§5.3).
- A shadow-mode report shows the numbers and lets the user mark the call right or wrong (S§5.4).
- A sanitizer rejection names the field and shows the `agent.toml` lines that would allow it (S§3.2).
- After a server is removed, the UI lists the values to rotate at their source (S§3.11).
- Once a quarter the UI asks for a recovery-key check, and it states the cost of losing the key in plain words (S§3.11).
- A new server's enrolment into an unprotected environment is confirmed with step-up (S§3.11).
- Creating an environment shows the `moonglass` CLI command that creates it; the UI cannot make its keys (ADR-0003). An org's first environment shows as protected from the start (ADR-0006).

## 7. Motion

At most 150 ms, ease-out. Motion shows a change of state or position and nothing else. With `prefers-reduced-motion`, transitions are replaced by an immediate change. Nothing animates in a loop.

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

The age library for browser-side encryption loads only on screens that save a secret (B W4-5).

## 11. Security

- **A strict Content Security Policy,** sent by the Go server as a header: neither `unsafe-inline` nor `unsafe-eval`. SvelteKit's one start-up script is allowed by its hash; no other inline script exists (B W2-S, B W4-5).
- **No third-party scripts or fonts at run time.** The UI uses system fonts and makes no request to any other origin.
- **Secret inputs are write-only** and never echoed back, logged or kept in client state after the save (S§3.11).
- **Destructive actions need a typed confirmation or an approval,** never a single click or a single key.
- Sessions use `__Host-` cookies; the UI stores no token where page script can read it.
- The UI states the limit of browser-side encryption wherever a secret is entered: it trusts the JavaScript we serve, and the CLI is the stronger path (S§3.11, `docs/threat-model.md` §5).

## 12. Accessibility

WCAG 2.2 AA. Text contrast of at least 4.5:1 in both themes, and non-text contrast of at least 3:1 for control boundaries, icons, state shapes and the focus ring, checked by a test over the token values that covers every pair in §5. Structural pane and row hairlines are exempt, for the reason given there. Everything is reachable and operable by keyboard, with a focus indicator on `:focus-visible` (§4). State is never conveyed by colour alone. Live regions announce deploy state changes; the inspector is a labelled `aside` landmark and is not a live region, so telemetry updates are not announced. Targets are at least 24 by 24 px.

## 13. Open choices

The owner accepted the default stated in each item on 2026-11-16. Each stays listed because the first screens may show it should change.

1. **Highlight versus open.** On a collection route, `j`/`k` highlight a row and the detail follows it without changing the URL; `Enter` opens the resource route and drops the list. The alternative is that selecting a row navigates, which would remove the list during a triage run through approvals.
2. **Route kinds.** Only deploys and approvals were named as deep routes. The service and server routes are classed as resource routes too, and the project routes as collections, so every service and server has an inspector. Audit and settings are pages with none.
3. **Ultra-wide default.** The inspector is closed by default on collection routes at 1440 px and up, because the request says "when toggled open". Opening it by default there is the other reading. Docked, it forces the list to 320 px.
4. **Toggle keys.** `⌘I` or `]`, both kept. Some browsers bind `⌘I` or Ctrl+I to their own features, so the handler cancels the default. `]` needs AltGr on some keyboard layouts, as `/` and `.` already may elsewhere.
5. **Hairlines are faint.** At 1.1–1.3:1 the pane dividers are a quiet cue, not a conformance claim. Region identity rests on landmarks, and dark mode adds a surface step between frame and canvas. In light the frame (step 1) is lighter than the canvas (step 2), the reverse of dark. Light is unchanged here.
6. **Rounded rows and divider lines.** Rows keep the 8 px radius with a hairline between them. Hiding the line beside the highlight is a rule to try in the first screens; if it looks fussy, drop the row dividers and keep the pane ones.
7. **Property strip.** It drops the sparklines; current values stay as text. A compact sparkline in the strip is possible if the first screens show it is wanted.
8. **Step 8 and 9 roles changed.** Step 8 cannot reach 3:1 in either theme, so icons and control borders moved to step 9 and step 8 is disabled only. The light accent on step 4 is 4.28:1, below 4.5, so accent text sits on steps 1–3. Both were already true of the old values.
