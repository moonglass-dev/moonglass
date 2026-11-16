# UI foundations

_W0-2 · 2026-11-16 · Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§). Every route, key binding, token value and budget below is _Proposed_ unless a source is cited; the owner confirms them in review._

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
| `/sign-in` | GitHub and email link on hosted; GitHub and the login-link instruction on self-hosted (ADR-0005) |
| `/{org}` | Redirects to `/{org}/projects` |
| `/{org}/projects` | Project list |
| `/{org}/projects/{project}` | A project: its environments and services |
| `/{org}/projects/{project}/{env}` | An environment: services, what is live, protected flag, secrets (names only) |
| `/{org}/projects/{project}/{env}/{service}` | A service: current release, deploy history, logs, current usage, settings |
| `/{org}/projects/{project}/{env}/{service}/deploys/{deploy}` | One deploy: steps, logs, diff, rollback |
| `/{org}/servers` and `/{org}/servers/{server}` | Servers: online state, key fingerprint, disk, what runs there |
| `/{org}/approvals` and `/{org}/approvals/{approval}` | Pending and past approvals, each with its diff |
| `/{org}/audit` | The audit trail |
| `/{org}/settings/{section}` | Members, tokens and agent identities, sessions, keys, notifications |

SvelteKit's static output is one app shell; the server returns it for every UI route and the client routes from there.

## 3. Layout

Three regions: **sidebar** (organisation switcher, navigation, pending-approval count), **list** (the collection for the current route) and **detail** (the selected item).

- **Wide, 1200 px and up:** all three. Sidebar 232 px, list 320–440 px, detail takes the rest.
- **Medium, 768–1199 px:** the sidebar collapses to a 48 px icon rail; list and detail stay side by side.
- **Narrow, under 768 px:** one column. The list is the page; opening an item replaces it with the detail and a back control. The sidebar becomes a drawer.

The detail header always answers three things first: what is live, since when, and who or what deployed it. Selection, scroll position and filters survive navigating away and back.

## 4. Keyboard model

- **⌘K (Ctrl+K) opens the command palette.** Its commands are generated from the operation registry, and it shows only what the signed-in user may run (B W2-S, ADR-0006). The server supplies that list; hiding a command is a convenience, and the registry still enforces every call. The palette also searches projects, services and servers by name.
- **`g` then a letter navigates:** `g p` projects, `g s` servers, `g a` approvals, `g l` audit log, `g o` organisation settings.
- **Single keys inside lists:** `j`/`k` or the arrow keys move, `Enter` opens, `Esc` goes back or clears, `/` filters, `x` selects, `.` opens the row's actions.
- **`?` opens a sheet of every shortcut,** generated from the same table the bindings use, so it cannot drift.
- **Focus is always visible:** a 2 px accent ring with a 2 px offset on every focusable element, in both themes. Focus moves into a dialog when it opens and back to its trigger when it closes.
- Single keys do nothing while a text input has focus, and never run a destructive action directly; they open its confirmation.
- A setting turns single-key shortcuts off, as WCAG 2.2 criterion 2.1.4 requires.

## 5. Design tokens

All values are _Proposed_. Components use semantic tokens only (`surface`, `text`, `border`, `accent`, `state-*`); the light and dark themes are two sets of values for the same names. The theme follows the operating system until the user chooses.

**Type scale** (size / line height, px): 11/16, 12/16, **13/20 (base)**, 14/20, 16/24, 20/28, 24/32. Weights 400, 500 and 600. A monospace face at 12/18 for logs, digests, fingerprints and identifiers.

**Spacing:** a 4 px grid: 0, 4, 8, 12, 16, 24, 32, 48, 64.

**Radius:** 4 px (inputs, pills), 8 px (buttons, rows, cards), 12 px (dialogs, palette).

**Neutral grey ramp**, lightness in OKLCH, chroma 0.01 or less, with a slight cool tint:

| Step | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Light | 0.99 | 0.975 | 0.955 | 0.93 | 0.90 | 0.86 | 0.80 | 0.70 | 0.56 | 0.48 | 0.40 | 0.20 |
| Dark | 0.16 | 0.19 | 0.22 | 0.25 | 0.28 | 0.32 | 0.38 | 0.47 | 0.56 | 0.63 | 0.74 | 0.94 |

Steps 1–2 are surfaces, 3–4 hover and selected, 5–7 borders, 8–9 icons and disabled, 10–12 text. Text uses steps 10–12 only.

**Accent:** one hue, used for the focus ring, the primary button, the selected navigation item and links. Draft: a muted sea-glass teal, `oklch(0.52 0.09 195)` on light and `oklch(0.76 0.10 195)` on dark, dark enough on light to carry link text and a white button label (open question 2).

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
| List row | One line: state shape, name, one secondary fact, relative time. Fixed height, fully keyboard driven |
| Status pill | State shape plus label, in the state hue. The only place state colours appear |
| Diff view | What a deploy or approval will change: image, config, environment variable names, services that will restart. Secret values never appear |
| Log stream | Live, monospace, virtualised; pauses following when the reader scrolls up |
| Command palette | §4 |
| Approval card | Who or what asked, the diff, the environment, and Approve or Decline. Approve starts step-up (ADR-0005) |
| Secret input | Write-only field. After saving it shows only that a value is set and when (S§3.11) |
| Typed confirmation | An Alert Dialog whose action is enabled once the resource's name has been typed |

**Surfaces the plan requires:**

- Before confirming a deploy, the UI flags stop-then-start shapes and lists the Compose services that will restart (S§5.3).
- A shadow-mode report shows the numbers and lets the user mark the call right or wrong (S§5.4).
- A sanitizer rejection names the field and shows the `agent.toml` lines that would allow it (S§3.2).
- After a server is removed, the UI lists the values to rotate at their source (S§3.11).
- Once a quarter the UI asks for a recovery-key check, and it states the cost of losing the key in plain words (S§3.11).
- A new server's enrolment into an unprotected environment is confirmed with step-up (S§3.11).

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

All _Proposed_; W2-S measures them in CI against the built output.

| Budget | Value |
|---|---|
| Initial JavaScript, gzipped, for the first authenticated screen | 90 KB or less |
| Route change with cached data, input to paint | 50 ms or less |
| Layout shift | None: cumulative layout shift of 0 |
| The static build embedded in the binary, compressed, fonts included | 1 MB or less |

The age library for browser-side encryption loads only on screens that save a secret (B W4-5).

## 11. Security

- **A strict Content Security Policy with no inline scripts,** and neither `unsafe-inline` nor `unsafe-eval` for scripts (B W2-S, B W4-5). See open question 5.
- **No third-party scripts or fonts at run time.** Fonts are self-hosted and embedded; the UI makes no request to any other origin.
- **Secret inputs are write-only** and never echoed back, logged or kept in client state after the save (S§3.11).
- **Destructive actions need a typed confirmation or an approval,** never a single click or a single key.
- Sessions use `__Host-` cookies; the UI stores no token where page script can read it.
- The UI states the limit of browser-side encryption wherever a secret is entered: it trusts the JavaScript we serve, and the CLI is the stronger path (S§3.11, `docs/threat-model.md` §5).

## 12. Accessibility

WCAG 2.2 AA. Text contrast of at least 4.5:1 and non-text contrast of at least 3:1 in both themes, checked by a test over the token values. Everything is reachable and operable by keyboard, with a visible focus indicator. State is never conveyed by colour alone. Live regions announce deploy state changes. Targets are at least 24 by 24 px.

## Open questions for the owner

1. **Typeface.** Options: (a) the system UI font stack, which costs no bytes and looks different on each OS; (b) one self-hosted variable sans with an open licence, which looks the same everywhere and costs tens of kilobytes against the embedded-build budget. Inter is excluded because Linear uses it. The same choice is needed for the monospace face.
2. **Accent colour.** The draft is a sea-glass teal. A blue would sit too close to the Building state and an amber too close to Waiting for approval; indigo is Linear's. The owner picks the hue; the contrast test then fixes the exact values.
3. **Logo and mark.** None exists. The sidebar and the sign-in screen need a mark that works at 16 px in one colour.
4. **Density.** One row height for every list: 32 px (most rows per screen), 36 px, or 40 px (calmest). The draft type scale assumes 36 px. A user setting for density would be a second way to do one thing, so none is proposed.
5. **CSP and SvelteKit's start-up script.** SvelteKit's static output starts the app from a small inline script and expects it to be allowed by hash. "No inline scripts" can mean (a) no `unsafe-inline`, with that one script allowed by its hash in the header the Go server sends, or (b) no inline script at all, which needs a build step to move it into a file. W2-S needs the choice.
6. **Identifiers in routes.** `{org}`, `{project}` and the rest can be readable slugs or opaque IDs. Slugs read better and break links on rename; IDs are stable and say nothing.
7. **`{org}` on self-hosted.** If a self-hosted install has exactly one organisation (ADR-0002, open question 5), the prefix could be dropped there, at the cost of two route shapes.
