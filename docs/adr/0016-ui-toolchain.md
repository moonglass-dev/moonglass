# ADR-0016: UI toolchain

- Status: accepted
- Date: 2026-11-17
- Amended: 2026-11-19, decision 3: SvelteKit 3.2 passes kit options, including the adapter, via `sveltekit(...)` in `ui/vite.config.ts` rather than `svelte.config.js` (owner decision, ticket W2-Sb unblocked)
- Amended: 2026-11-19, decision 5: self-hosted Geist and Geist Mono font assets (owner decision, W2-Sa split)

## Context

Stack §3.16 chooses Svelte 5, SvelteKit in static mode, Tailwind and Bits UI, compiled to static files and embedded in the binary. Stack "Not verified" left the exact versions open. No document names the JavaScript runtime, package manager or build integration, and `AGENTS.md` requires an ADR for every new dependency. W2-S builds the first screens and now depends on W4-1 (sign-in), so it starts in week 4.

Versions were checked upstream on 2026-11-17 (the package registry and the `oven-sh/bun` releases). Each pin is the newest stable release published at least seven days earlier, so a release that is pulled in its first week is never picked up.

## Decision

1. **Runtime and package manager:** Bun **1.4.4** installs the dependencies and runs the build. Bun is pinned in `ui/package.json` with `"packageManager": "bun@1.4.4"`. `ui/bun.lock` (the text lockfile) is committed and installs use `bun install --frozen-lockfile`; build and check scripts run the tools through `bunx --bun`. npm, pnpm and Yarn are not used.
2. **Exact pins** in `ui/package.json`, without `^` or `~`:

   | Package | Version | Role |
   |---|---|---|
   | `svelte` | 5.57.3 | Compiler and runtime |
   | `@sveltejs/kit` | 3.2.0 | App framework, static output |
   | `@sveltejs/adapter-static` | 4.0.1 | Static build |
   | `@sveltejs/vite-plugin-svelte` | 7.3.1 | Vite integration |
   | `vite` | 8.3.7 | Build tool |
   | `tailwindcss` | 4.3.5 | Styles |
   | `@tailwindcss/vite` | 4.3.5 | Tailwind's Vite plugin |
   | `bits-ui` | 2.20.6 | Primitives (`docs/specs/ui.md` §6) |
   | `@internationalized/date` | 3.12.4 | Peer dependency of `bits-ui` |
   | `typescript` | 6.0.3 | Type checking (Kit 3.2 and `svelte-check` require 6) |
   | `svelte-check` | 4.7.8 | Type and template checks |

   Adding or upgrading a package is a change to this ADR, in one ticket.
3. **Build integration.** `make ui` runs `bun install --frozen-lockfile` and `bun run build` in `ui/`. SvelteKit 3.2 takes its options — including the adapter — from the `sveltekit(...)` plugin call in `ui/vite.config.ts`, not from `svelte.config.js`; `ui/svelte.config.js` does not exist. `@sveltejs/adapter-static` is configured there to emit its pages and assets to `../internal/ui/dist`, which is git-ignored and embedded with `//go:embed` by `internal/ui`. `make build`, `make build-all`, `make test`, `make lint` and `make ci` run `make ui` first, so the binary always embeds a fresh build, and no build output is committed. CI installs Bun 1.4.4 with a pinned `oven-sh/setup-bun` (`bun-version: 1.4.4`).
4. **Budgets in CI** (`docs/specs/ui.md` §10) are checked without a browser: initial JavaScript gzipped and the compressed embedded build are measured from the build output. The route-change and layout-shift budgets need a browser. They are not measured in CI until a ticket adds a headless browser under its own ADR, and until then they are checked in the weekly demo.
5. **No runtime requests and self-hosted fonts** (`docs/specs/ui.md` §5, §11) (amended 2026-11-19). No runtime requests are made to any other origin or third-party CDN. Geist and Geist Mono are approved and self-hosted as static assets:
   - Upstream source: Vercel Geist and Geist Mono.
   - License: SIL Open Font License 1.1.
   - Format: Latin-subsetted variable WOFF2 fonts embedded in the static build (~65 KB total, 0 KB JavaScript).
   - Delivery: Loaded via local `@font-face` with `font-display: swap` and system fallbacks (`system-ui` and `ui-monospace`) so the first paint does not shift (CLS 0).
   - Invariant: Zero network requests to third-party CDNs or origins at runtime.

## Consequences

- `make ci` needs Bun 1.4.4 on the machine. CI provides it, and developers install it once.
- A clean clone builds the binary only after `make ui`. `go build` alone fails at the embed with a message pointing to `make ui`.
- Dependency updates are deliberate and reviewed, like Go modules.
- Self-hosted Latin-subsetted Geist and Geist Mono WOFF2 font files (~65 KB together) count toward the 1 MB static build budget (§10); zero third-party CDN or font requests are made at runtime.

## Alternatives rejected

- **npm, pnpm or Yarn.** Each adds a toolchain to install and pin beside Bun, which already covers install and build.
- **Commit the built UI.** Large diffs on every change, and a stale build could ship.
- **Caret ranges.** A fresh install could pull an unreviewed release.
- **A headless browser in W2-S.** A large dependency before any screen needs it.

## Open questions

None.

## Links

- `docs/plan/stack.md` §3.16; `docs/specs/ui.md` §10, §11
- B§6: W2-Sb, W2-Sc, W2-Sa, W2-S, W4-5, W4-6, W7-7
