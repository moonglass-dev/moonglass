# UI foundations

_Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§). The spec for the dashboard's routes, layout, keyboard model, tokens, components, flows, motion, live data and budgets. Every dashboard ticket's acceptance criteria name the sections it meets._

The stack is fixed by S§3.16: Svelte 5, SvelteKit in static mode, Tailwind and Bits UI, compiled to static files and embedded in the Go binary. The dashboard matches moonglass.dev: calm, Linear-like, dark by default and dense. It borrows Linear's discipline (a recessed sidebar framing an inset rounded canvas, flat hairlines, generous whitespace on resource pages, keyboard-first flow) while keeping Moonglass's own palette, glass orb mark, typography and deploy workflows.

**Dashboard first.** Every capability is fully doable here: deploys, secrets entry, reveal and rotate, environments and keys, servers, approvals, restore and settings. The CLI, the API and MCP mirror the same registry operations (ADR-0006). A feature is done only when its flow in §2.1 ships.

## 1. Principles

Each principle is a pass/fail check in design and code review.

1. **Complete.** Every operation a person may run has a dashboard flow. A flow that needs the CLI is a defect.
2. **Keyboard complete.** Every operation is reachable without a pointer via `⌘K`, `g` chords or single-key list bindings, and every focusable element shows `:focus-visible`.
3. **Instant over spinners.** Cached data renders on frame 1 (≤50 ms route transition); loading skeletons never appear before 150 ms.
4. **Colour means state, never decoration.** One accent hue marks focus, the primary action and active selection; semantic hues appear only on status dots, state labels and diff lines. No decorative gradients, hero banners or card grids.
5. **Numbers over adjectives.** Copy states measurable facts ("0 failed requests across 10 deploys", "42s build", "23 requests finishing on v41"), never marketing words. No war words; "coding agent" or "Moonglass agent", never a bare "agent" in prose (V§1.2). The sidebar item **Agents** means coding agents.
6. **Preview consequences before confirmation.** Any action that restarts, stops, deletes or needs approval shows the exact diff and the affected services before the confirm step (S§5.3).

## 2. Information architecture

The hierarchy is organisation → projects → environments → services → deployments. The Inbox, Activity, Servers, Agents and Settings sit beside it at the organisation level.

| Route | Shows | Ticket |
|---|---|---|
| `/sign-in` | GitHub, Google, OpenID Connect and passkey buttons for the methods the operator set up, and the login-link instruction for emergencies (ADR-0005, `docs/specs/auth.md`) | W4-1f, W4-1b, V1-6, V1-7 |
| `/first-run` | The first-run flow (§2.1) | W7-7, AL-2 |
| `/{org}` | Redirects to `/{org}/inbox` once the Inbox exists, else `/{org}/projects` | W2-S, AL-1 |
| `/{org}/inbox` | What needs this person: pending approvals with their diff inline, failed deploys, rollbacks and shadow-mode reports, servers offline, disk low, backups failed, billing notices (trial ending, lapse, idle-pause warning, resume after idle pause) | AL-1, LN-2, LN-7, W4-7, W7-4 |
| `/{org}/activity` | The audit trail: every operation, by person or coding agent, newest first | LN-8 |
| `/{org}/servers` and `/{org}/servers/{server}` | Servers: online state, fingerprint, disk, usage, what runs there; Add server | W2-S, AL-3 |
| `/{org}/agents` and `/{org}/agents/{agent}` | Coding agents: identities, their tokens and scopes, their deploys and approvals; the MCP address | LN-6 |
| `/{org}/projects` | Project list | W4-6 |
| `/{org}/projects/{project}/{env}/{tab}` | A project in one environment, with the tabs Overview, Deployments, Logs, Variables, Settings | W4-6 |
| `/{org}/projects/{project}/{env}/services/{service}/{tab}` | A service, with the same five tabs | W4-6 |
| `/{org}/projects/{project}/{env}/deployments/{deployment}` | One deployment: its steps, logs, diff, approval and rollback | W4-6, LN-7 |
| `/{org}/settings/{section}` | Members, sign-in, notifications, keys, backups, organisation, billing (Cloud and Business), about and update | LN-4, LN-1, AL-2, LN-11, LN-10, AL-5 |

Every `{…}` segment is the resource's opaque, stable ID, never its name, except `{tab}` and `{section}`, which are fixed words. Names are display text, so a link survives a rename. `{org}` is in every route on Cloud and self-hosted, so there is one route shape (ADR-0002: a control plane may hold several organisations).

SvelteKit's static output is one app shell; the server returns it for every dashboard route and the client routes from there. A sidebar item appears with the ticket in the table that gives it something to show; there are no placeholder items.

### 2.1 Dashboard flows

Every capability and the flow that does it. Each flow is driven from the keyboard in its milestone's gate (B§2).

| Flow | Where | What it does | Ticket |
|---|---|---|---|
| **First run** | `/first-run` | Sign in; create the recovery key in the browser, save it, prove the saved copy unwraps a test value; connect GitHub (the App from its manifest); pick a repository; set variables; deploy; watch it go live | W7-7, AL-2 |
| **Services** | Project → New service | From a connected repository and branch, an image, a Dockerfile, Railpack, a Compose file or a template; port, health check path, domains with the DNS record to add and a resolve check; auto-deploy on push | AL-4, LN-12, LN-14, LN-23 |
| **Deploy and roll back** | Deployments tab, ⌘K | Deploy a commit or image; watch Pull, Health check, Switch traffic, Drain; Roll back to any kept release | AL-4 |
| **Variables** | Variables tab | Set, replace and delete values, encrypted in the browser; Reveal with step-up; names only otherwise | W4-5, LN-11 |
| **Environments and keys** | Project → New environment; Settings → Keys | Create an environment and its key in the browser, wrapped to the recovery key and the servers picked; rotate an environment key; rotate the recovery key; the quarterly recovery-key check | AL-2, LN-11 |
| **Servers** | Servers → Add server | Show the enrolment token and install command (and, on Cloud, the new monthly price first: free for the first server, then $5 per extra), then the fingerprint once it enrols; grant it to environments by fingerprint or with the recovery key file; after removal, the values to rotate at their source | AL-3, LN-11, W7-4 |
| **Approvals** | Inbox, Deployments tab | Read the diff inline; Approve with step-up, or Decline; protect an environment in its Settings tab; approver rules | LN-7, V1-8 |
| **Coding agents** | Agents | Create an identity, mint a scoped token (shown once), revoke; copy the MCP address and a deploy-hook command | LN-6 |
| **Members** | Settings → Members | Invite with a role, copy the link, change a role, remove; switch and create organisations | LN-4 |
| **Databases** | Project → New database | Create Postgres, MySQL, MariaDB, MongoDB or Redis; reveal its URL with step-up; schedule backups to a bucket whose credentials are encrypted in the browser; Restore with a typed confirmation | LN-10 |
| **Previews** | Environment switcher; project Settings | Turn previews on per repository; pick the base environment, the wildcard domain and the database choice; open a preview from its pull request | LN-18 |
| **Compose** | Project → New → Compose | From the repository or pasted; the plan list before anything changes; Apply | LN-14 |
| **Templates** | Project → New → Template | Search the gallery, fill in variables, see the plan, deploy | LN-23 |
| **Logs and metrics** | Logs tab, runtime panel, Overview tab | Live logs that pause on scroll; a time range and text filter; charts of CPU, memory, network and requests | W4-6, LN-19, LN-20 |
| **Auto-rollback** | Service Settings tab, Inbox | Triggers and thresholds; mark a shadow-mode report right or wrong | LN-2 |
| **Notifications** | Settings → Notifications | Webhook, Slack, Discord, Telegram and email channels, their events, a test send | LN-1 |
| **Delete and restore** | Settings tab of a service, environment or project | Delete with a typed confirmation, restore within 7 days, purge with step-up | W5-7 |
| **Activity** | Activity | Filter by project, environment, person, coding agent and operation; expand an event to its ids and hashes; the `moonglass audit verify` command, with no verified badge (`docs/specs/audit.md`) | LN-8 |
| **Upgrade** | Settings → About | See a newer release on the channel; Update; see the revert if the self-check fails | AL-5 |
| **Import and export** | Settings → Organisation | Import from Coolify; export the control plane | W7-3, W7-2 |
| **Container terminal** | A service's screen | A terminal into a running container, with step-up | V1-10 |
| **Billing** | Settings → Billing (Cloud and Business only) | The plan, the founding term if any, the servers counted (one free, then $5 each extra), the next invoice, the card, invoices, idle-pause state if it applies, and the change and cancel buttons (`docs/specs/billing.md`) | W7-4 |

## 3. Layout

The shell is two panes: a recessed sidebar and a single inset rounded canvas (8 px radius, 8 px margin on the top, right and bottom, 1 px solid `--border-subtle` border). Resource routes open the live **runtime panel** inside the right edge of that canvas. There is no list-and-detail split.

**Sidebar** (220 px; 48 px icon rail below 1200 px), top to bottom:

1. The 22 px Moonglass orb and the "Moonglass" word mark, then the organisation switcher (its name; a menu with the other organisations, New organisation and Settings).
2. **Inbox**, with its unread count. **Activity**. **Servers**, with the number online. **Agents**. **Projects**, with the organisation's projects listed beneath it, each with a status dot for its worst current state.
3. At the bottom, the control that opens `⌘K`, and the signed-in person's menu (sessions, sign out, theme).

**Route kinds.** Every route in §2 has exactly one kind.

| Kind | Routes | Canvas (≥1200 px) |
|---|---|---|
| Collection | `/{org}/inbox`, `/{org}/activity`, `/{org}/servers`, `/{org}/agents`, `/{org}/projects` | A full-width list inside the inset canvas, with no runtime panel |
| Resource | A project, a service, a deployment, a server, an agent | Main content, with the runtime panel (280 px, `--runtime-panel-width`, 1 px `--border-subtle` left hairline) at the right edge of the canvas |
| Page | `/first-run`, `/{org}/settings/{section}` | One full-width page inside the inset canvas; no runtime panel |

On a collection route, `j`/`k` highlight a row; `Enter` opens the row as its own resource route, or expands it in place where §6 says so (an Inbox approval); `Esc` returns to the collection with the row still highlighted. Selection, scroll position and filters survive navigating away and back.

**Widths.**

| Viewport | Collection or page route | Resource route |
|---|---|---|
| **1200 px and up** | Sidebar 220 px, inset canvas `1fr` | Sidebar 220 px, inset canvas as `1fr` main (minimum 600 px) plus the 280 px runtime panel, open by default, toggled with `⌘I` or `]` |
| **768–1199 px** | Sidebar collapses to a 48 px icon rail; inset canvas `1fr` | Rail and inset canvas; the runtime panel's values appear in the property strip |
| **Under 768 px** | One full-bleed column (the canvas margin and radius drop to 0). The list is the page; opening an item replaces it with the resource and a back control. The sidebar becomes a drawer | One column, the resource with the property strip and a back control; the sidebar is a drawer |

**Canvas top bar.** No hero. 40 px high, with a 1 px bottom hairline:

1. A **breadcrumb** on the left: `Org › Project › Environment › Service`, ending at the open resource. The environment segment is the **environment switcher**: a menu of the project's environments and open previews, each with its status dot. Under 768 px the bar shows the back control and the last segment.
2. At most two contextual quick actions on the right (on a service: Deploy and Roll back), plus the row-actions menu (`.`) and, on a resource route, the runtime-panel toggle. Quick actions come from the same server-supplied list as the palette (§4). A destructive one opens its confirmation (§11).

**Tabs** (projects and services). A 40 px bar of pill tabs under the top bar, always these five, in this order:

| Tab | On a project (one environment) | On a service |
|---|---|---|
| **Overview** | Every service with its status dot, current release, domains and usage; New service, database, Compose or template | Current release, domains, health, the last deployments, CPU and memory charts |
| **Deployments** | Every deployment in the environment, newest first, with a pending approval pinned at the top with its diff inline | The service's deployments, the same way |
| **Logs** | All services' logs, interleaved, filterable by service | The service's live logs; a time range and a text filter |
| **Variables** | The environment's variables: names, when each was set and by whom; set, replace, delete and reveal | The same list, marking which the service uses at build and at run time |
| **Settings** | The environment: protection, approver rules, previews, servers it is granted to, keys, deletion | Source and build, port and health check, domains, auto-rollback, resources, the server it runs on, deletion |

A server and a coding agent have their own tabs (a server: Overview, Services, Logs, Settings; an agent: Overview, Tokens, Activity). The active pill moves in 160 ms (§7). The number keys `1` to `5` select a tab.

**Resource header.** Inside the canvas, with 32 px padding: the title (`--text-lg`, 22 px, weight 600) with its status dot and environment tag (for example `Production`), a one-line summary in `--text-secondary`, then an inline Properties row (a 100 px muted label column and value chips: live status, active commit, target server, environment protection, last deployed by and when).

**Runtime panel.**

- It holds live, passive information only, updated over server-sent events (§9): the status dot and state label; the live release (version or commit, image digest) and how long it has been live; the server it runs on, with CPU, memory and disk; requests per second, p95 latency and error rate from the proxy's counters, each with a sparkline and its current value as text; the domains with their certificate state.
- **While a deployment is in flight** it shows the steps (Pull, Health check, Switch traffic, Drain) with the current one marked, and the drain count ("23 requests finishing on v41"); when it ends, the result in numbers ("v42 is live · 6.0 s · no dropped requests", or "Back on v41 · 0.3 s · no rebuild").
- **After a switch**, the new and the previous release side by side for the error-rate window, the same numbers auto-rollback watches (S§5.4).
- On a project, it lists each service with its status dot and state; on a server, the server's usage and the services on it.
- It may link to a related resource, such as the server. It has no control that changes state; everything interactive stays in the main canvas: the log stream, the diff view, and Approve and Decline.
- Layout is flat: a 1 px left hairline (`--border-subtle`, §5), 20 px padding, label and value pairs in one column. No nested cards or boxes. Labels are `--text-sm` in `--text-tertiary`; values are `--text-base` in `--text-primary`; identifiers are monospace `--text-sm` at 12/18. Groups are separated by 16 px of space, not by rules.
- Each sparkline is shown with its current value as text, and the graphic is hidden from assistive technology (§12).
- **Property strip.** Below 1200 px on a resource route, the same values appear as a compact horizontal strip between the resource header and the content. It wraps onto a second line rather than scrolling, shows current values as text, and has no sparklines.
- **Toggle.** `⌘I` or `]` (§4). The open or closed state is stored per viewport class in the browser's local storage, as a preference that is never sent to the server. Classes are 768–1199 px and 1200 px and up, for resource routes. The state is read before the shell first renders, so toggling and loading never shift layout (§10). On a collection or page route the toggle does nothing, and its palette entry is disabled with that reason.

**Dividers.** The sidebar and the canvas are separated by the 8 px frame margin; the runtime panel is separated from main content by a 1 px `--border-subtle` line, never by a shadow. Each internal pane draws one such line on its leading edge, except the first, so a boundary is never doubled.

```
Inbox (collection)                                   ≥1200 px

+------------------+-----------------------------------------------------+
| (o) Moonglass    | acme › Inbox                          Mark all read |
|  acme         v  |-----------------------------------------------------|
|                  |  ‖  web-dev wants to deploy v42 to Production   2m  |
|  Inbox        2  |     coding agent over MCP · live on Staging 2h,     |
|  Activity        |     no errors                                       |
|  Servers      3  |     image   ghcr.io/acme/web:v41 → v42              |
|  Agents          |     secret  + SEARCH_INDEX_URL                      |
|  Projects        |     restart web (blue/green)                        |
|   ● web          |                               [ Reject ] [ Approve ] |
|   ● api          |  ×  api: deploy #183 failed at Health check     14m |
|   ◐ billing      |  ○  vps-ams-2 offline since 09:12               1h  |
|  [⌘K]            |                                                     |
+------------------+-----------------------------------------------------+
 220 px             inset canvas, 8 px radius and margin
```

```
Service (resource)                                   ≥1200 px

+------------------+------------------------------------+----------------+
| (o) Moonglass    | web › Production v › web   Deploy  Roll back  .  [ ]|
|  acme         v  |------------------------------------+----------------|
|                  | (Overview) Deployments Logs        | ● Live         |
|  Inbox        2  |  Variables Settings                | v42 · 2m       |
|  Activity        |                                    | vps-fra-1      |
|  Servers      3  | ● web   Production                 | 184 req/s ~~~  |
|  Agents          | acme.com, www.acme.com             | p95 41 ms ~~~  |
|  Projects        |                                    | errors 0.1%    |
|   ● web          | Properties [● Live] #a8f31c2       | CPU 38% ~~~    |
|   ● api          |            vps-fra-1   2m ago      | RAM 2.1/8 GB   |
|  [⌘K]            | Recent deployments                 | Disk 61%       |
+------------------+------------------------------------+----------------+
 220 px             main (1fr, min 600 px)               runtime panel 280 px
```

## 4. Keyboard model

- **⌘K (Ctrl+K) opens the command palette.** Its commands are generated from the operation registry, and it shows only what the signed-in person may run (B W2-S, ADR-0006). The server supplies that list; hiding a command is a convenience, and the registry still enforces every call. The palette also searches projects, services, servers and coding agents by name. Alongside the registry commands it lists the client-only ones, such as "Toggle runtime panel"; they come from the shortcut table below, carry no authority and make no registry call.
- **`g` then a letter navigates:** `g i` Inbox, `g a` Activity, `g s` Servers, `g c` coding agents (Agents), `g p` Projects, `g ,` Settings.
- **Single keys inside lists:** `j`/`k` or the arrow keys move, `Enter` opens or expands, `Esc` goes back or clears, `/` filters, `x` selects, `.` opens the row's actions. In a log view, `G` jumps to the bottom and resumes follow. On an expanded approval, `a` starts Approve and `r` starts Reject; both open their confirmation, never act directly.
- **`1` to `5` select a tab** on a project or a service (§3).
- **`⌘I` (Ctrl+I) or `]` toggles the runtime panel** where there is one (§3). `⌘I` works while a text input has focus; `]` is a single key and follows the rule below. The handler cancels the browser's own binding for `⌘I`.
- **`?` opens a sheet of every shortcut,** generated from the same table the bindings use, so it cannot drift.
- **Focus is always visible to a keyboard user:** a 2 px solid accent ring with a 2 px offset, drawn on `:focus-visible` and on no other state, on every focusable element, in both themes. Keyboard navigation (`Tab`, `j`/`k`, `⌘K`) shows the ring; a pointer click on a button or row does not. Scroll containers keep 4 px of padding so the ring is never clipped. Focus moves into a dialog when it opens and back to its trigger when it closes.
- Single keys do nothing while a text input has focus, and never run a destructive action or an approval directly; they open its confirmation.
- A setting turns single-key shortcuts off, as WCAG 2.2 criterion 2.1.4 requires.

## 5. Design tokens

Components use semantic tokens only (`--surface-*`, `--text-*`, `--border-*`, `--accent`, `--state-*`); the light and dark themes are two sets of values for the same names. Dark is the default in `:root`. The theme follows the operating system until the user chooses. Exact CSS custom properties live in `docs/specs/ui-tokens.css`, and `ui/src/lib/tokens.css` is a byte-for-byte copy.

**Typefaces:** self-hosted Latin-subsetted WOFF2 variable fonts, Geist and Geist Mono, embedded in the static build (about 65 KB together, 0 KB of JavaScript, no request to any other origin). They are loaded with `font-display: swap` and system fallbacks (`system-ui` and `ui-monospace`) so the first paint does not shift (CLS 0). Inter is excluded. The files count toward the 1 MB embedded-build budget in §10.

**Mark:** a 22 px dark polished glass orb, a static SVG, at the top-left of the sidebar beside the "Moonglass" word mark (weight 600). It has a 1 px specular rim and quiet teal and blue strands painted inside the sphere. It does not glow, and it does not move (§7). Paint-server IDs in the SVG (`id` on a gradient or filter) must be unique per instance, so two orbs on one page do not clash; they must not be fixed global IDs.

**Type scale** (five sizes; size / line height, and tracking where it is not zero):

| Token | Size / line height | Tracking | Weight | Use |
|---|---|---|---|---|
| `--text-xs` | 11/16 | +0.02em | 500 | Section labels, keyboard hints, table headers |
| `--text-sm` | 12/16 (18 mono) | 0 | 400, 500 | Secondary metadata, runtime-panel labels, log lines, SHAs |
| `--text-base` | 13/20 | −0.005em | 400, 500 | Body, list rows, inputs, buttons, runtime-panel values |
| `--text-md` | 15/22 | −0.015em | 500, 600 | Dialog titles, section headings, empty-state titles |
| `--text-lg` | 22/28 | −0.025em | 600 | Resource and page titles |

Weights are 400, 500 and 600. `font-variant-numeric: tabular-nums` applies to timestamps, counts, durations and metrics.

**Spacing:** a 4 px grid: 0, 4, 8, 12, 16, 20, 24, 32, 48.

**Radius:** `--radius-sm` 4 px (buttons, inputs, chips, nav items, `kbd`); `--radius-md` 6 px (popovers, dropdowns, approval cards); `--radius-lg` 8 px (inset canvas, dialogs, the `⌘K` palette); `--radius-full` 9999 px (the orb, pill tabs, status dots, status pills). List rows are square (0 px) and run edge to edge.

**Elevation.** Borders over shadows. The inset canvas, the runtime panel and list rows use a 1 px `--border-subtle` line and no shadow. Only floating layers (dropdown, popover, dialog, `⌘K`) combine a 1 px `--border-strong` line with `--shadow-overlay`.

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

- `--border-subtle` (step 5): the structural hairline of the canvas, the runtime panel and list rows (§3, §6). No shadow divides panes or rows.
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

**Status dots.** Every list row, sidebar project, tab title and runtime panel carries a status dot: the state's 14 px glyph in the state's token (above), so state never depends on colour alone. Servers use Live's filled dot for online, Failed's cross for offline and Switching's hue with a filled dot for disk low. A project's dot is the worst state among its services. An approval waiting anywhere in a project shows Waiting for approval's pause bars on the project row.

## 6. Components

**Bits UI primitives used:** Command (the palette), Dialog, Alert Dialog, Dropdown Menu, Popover, Tooltip, Select, Tabs, Switch, Checkbox, Label, Separator. A primitive is added to this list before it is used.

**Composite components:**

| Component | What it is |
|---|---|
| List row | One line: status dot, name, one secondary fact, relative time. 36 px high in every list, with no density setting; fully keyboard driven. Rows are square and edge to edge, divided by a 1 px `--border-subtle` bottom edge that is inside the 36 px and turns transparent, rather than disappearing, on the highlighted, hovered and selected row and the row above, so no row changes height |
| Status dot | §5, "Status dots" |
| Canvas top bar | A 40 px breadcrumb bar with the environment switcher and quick actions on the right. §3 |
| Resource header | Title, status dot, environment tag, one-line summary and the Properties row, with 32 px padding. §3 |
| Pill tabs | Overview, Deployments, Logs, Variables, Settings. The active pill moves in 160 ms. §3, §7 |
| Runtime panel | The read-only live column of §3, with a 1 px `--border-subtle` left edge, 20 px padding and no nested boxes, inside the canvas. Below 1200 px it is the property strip |
| Sparkline | A small line of recent values inside the runtime panel, always beside its current value as text. It updates with the stream (§9) and never animates on its own |
| Status pill | Status dot plus label, in the state token, in the resource header and the Deployments tab |
| Deploy steps | Pull, Health check, Switch traffic, Drain (and Build first for a source build), each with its state glyph and elapsed seconds, in a deployment's screen and the runtime panel |
| Diff view | What a deployment or approval will change: the image or digest (old → new), the commit range with its messages, changed settings (port, health check, domains, resources), variable names added, changed or removed (`+`, `~`, `−`, never a value), and every service that restarts, switches blue/green or stops and then starts. Additions in `--state-success`, removals in `--state-danger`, both with their sign |
| Approval card | An inline approval: who or what asked (a person, or a coding agent and how: CLI, API or MCP), what (deploy v42 to Production), the evidence the control plane has (how long this release has been live in another environment, its error rate there), the diff view, and Reject and Approve. Approve starts step-up (ADR-0005) and then shows the deployment's steps in place. It appears expanded in place in the Inbox and pinned at the top of the Deployments tab, never only on a separate page |
| Log stream | Live, monospace, virtualised; pauses following when the reader scrolls up; a time range and a text filter (LN-20) |
| Chart | A line chart of one metric over a chosen range, with the current value as text and the data as a table for assistive technology (LN-19) |
| Command palette | §4 |
| Secret input | Write-only field. After saving it shows only that a value is set, when, and by whom (S§3.11). Reveal shows the value in place after step-up (§11) |
| Key file input | Reads a recovery key file into memory for one operation, never uploads it, and forgets it when the operation ends (ADR-0003 decision 14) |
| Typed confirmation | An Alert Dialog whose action is enabled once the resource's name has been typed |
| Empty state | What belongs here and the next action as a button with its shortcut (§8) |

The log stream, the diff view, the approval card and every other control live in the main canvas, never in the runtime panel.

Every interactive component has default, hover, active, focus, disabled and loading states.

**Surfaces the plan requires:**

- Before confirming a deploy, the dashboard flags stop-then-start shapes and lists the Compose services that will restart (S§5.3, `docs/specs/compose.md`, "The list").
- A shadow-mode report shows the numbers and lets the person mark the call right or wrong (S§5.4).
- A sanitizer rejection names the field and shows the `agent.toml` lines that would allow it (S§3.2).
- After a server is removed, the dashboard lists the values to rotate at their source (S§3.11).
- Once a quarter the dashboard asks for a recovery-key check, and it states the cost of losing the key in plain words (S§3.11).
- Granting a server an environment shows its fingerprint next to the one its enrolment printed, before the step-up (ADR-0003 decision 5).
- Wherever a key or a secret is handled, one line states the limit of browser-side encryption and links the CLI command that does the same (§11).
- On Cloud, adding a server states the new monthly price before the install command (`docs/specs/pricing.md`). The first server is free ("This is your free server: $0.00 a month"). A later server names the new total ("This makes 3 servers: $10.00 a month from today").
- On a free Cloud account, an Inbox item three days before an idle pause warns that the control plane will pause, and that signing in keeps it running. After a pause, the next login resumes the control plane; the Inbox records the resume (`docs/specs/billing.md`, "Idle pause"). A paused control plane cannot enrol a server or deploy until that login.

## 7. Motion

Motion is short and rare. It shows a change of state or position and nothing else.

- **State transitions** use `--duration-micro` (90 ms) for colour, opacity and hairline changes, and `--duration-move` (160 ms) for the active pill, the runtime panel opening or closing, and other small moves. The easing is `--ease-out` (`cubic-bezier(0.2, 0, 0, 1)`), with no overshoot.
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
| Deployment state and step progress, and the drain count | The Activity list |
| The log stream | Settings, members, tokens, sessions |
| Server online and offline, disk and usage | Variable names |
| The runtime panel's numbers: requests, latency, error rate, CPU, memory | Deployment history older than the current deployment |
| The Inbox: pending approvals, failures, offline servers, billing notices, and the unread count | Chart history beyond the last hour (LN-19) |
| Rollback and shadow-mode events | |

Streams are authorised like any other read. When a stream reconnects, the dashboard fetches the current state again and does not assume it missed nothing.

## 10. Performance budgets

W2-S measures the two size budgets in CI against the built output (ADR-0016). The route-change and layout-shift budgets need a browser; until a ticket adds one under its own ADR, they are checked in every gate's demo.

| Budget | Value |
|---|---|
| Initial JavaScript, gzipped, for the first authenticated screen | 90 KB or less |
| Route change with cached data, input to paint | 50 ms or less |
| Layout shift | None: cumulative layout shift of 0 |
| The static build embedded in the binary, compressed | 1 MB or less |

The age library (ADR-0018) loads only on screens that handle keys: setting, revealing or rotating a secret, creating or checking a key, and granting a server (W4-5, AL-2, AL-3, LN-11). Self-hosted font files count toward the 1 MB budget and must not add JavaScript or a network request to another origin.

## 11. Security

- **A strict Content Security Policy,** sent by the Go server as a header: neither `unsafe-inline` nor `unsafe-eval`. SvelteKit's one start-up script is allowed by its hash; no other inline script exists (B W2-S, B W4-5).
- **No third-party scripts or fonts at run time.** Geist and Geist Mono are self-hosted WOFF2 files in the static build and make no request to any other origin.
- **Secret inputs are write-only** and never echoed back, logged or kept in client state after the save (S§3.11). A revealed value is shown until the person leaves the screen or 60 seconds pass, whichever is first, and is never cached.
- **Keys stay in memory.** A recovery key file is read locally and never uploaded; an unwrapped environment key exists only for the operation that needs it (ADR-0003 decision 14).
- **Destructive actions need a typed confirmation or an approval,** never a single click or a single key.
- Sessions use `__Host-` cookies; the UI stores no token where page script can read it.
- The UI states the limit of browser-side encryption wherever a secret is entered: it trusts the JavaScript we serve, and the CLI is the stronger path (S§3.11, `docs/threat-model.md` §5).

## 12. Accessibility

WCAG 2.2 AA. Text contrast of at least 4.5:1 in both themes, and non-text contrast of at least 3:1 for control boundaries, icons, state shapes and the focus ring, checked by a test over the token values that covers every pair in §5. Structural pane and row hairlines are exempt, for the reason given there. Everything is reachable and operable by keyboard, with a focus indicator on `:focus-visible` (§4). State is never conveyed by colour alone. Live regions announce deploy state changes; the runtime panel is a labelled `aside` landmark and is not a live region, so telemetry updates are not announced. Targets are at least 24 by 24 px.

## 13. Open choices

Each item stays listed because the first screens may show it should change.

1. **Toggle keys.** `⌘I` or `]`, both kept. Some browsers bind `⌘I` or Ctrl+I to their own features, so the handler cancels the default. `]` needs AltGr on some keyboard layouts, as `/` and `.` already may elsewhere.
2. **Hairlines are faint.** At about 1.1:1 the pane dividers are a quiet cue, not a conformance claim. Region identity rests on landmarks, and both themes keep the frame deeper than the canvas.
3. **Row divider lines.** Rows are square and edge to edge, with a hairline between them. Hiding the line beside the highlight is a rule to try in the first screens; if it looks fussy, drop the row dividers and keep the pane ones.
4. **Property strip.** It drops the sparklines; current values stay as text. A compact sparkline in the strip is possible if the first screens show it is wanted.
5. **Projects in the sidebar.** Every project is listed under Projects. With more than about fifteen, the list may need to show only starred and recent projects.
