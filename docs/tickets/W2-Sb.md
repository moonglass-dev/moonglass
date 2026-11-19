---
id: W2-Sb
title: SvelteKit scaffold
status: todo
risk: standard
depends_on: [W0-2]
allowed_paths:
  - ui/
max_diff_lines: 400
tests:
  - ./ui TestScaffoldFilesPresent
  - ./ui TestPackageJSONExactPins
  - ./ui TestViteConfigAdapterStatic
  - ./ui TestViteConfigTailwind
reads:
  - docs/adr/0016-ui-toolchain.md
  - docs/specs/ui.md
  - AGENTS.md
---

# SvelteKit scaffold

Scaffold the SvelteKit application in `ui/` per ADR-0016 (`docs/adr/0016-ui-toolchain.md`). This establishes the UI project structure, exact dependency pins, build configurations, and verification Go tests in `./ui` before any design tokens or components are added.

This was split out of W2-Sa (owner decision, 2026-11-19) to keep ticket diffs within size limits.

## Scope

- Create `ui/package.json` pinned to `"packageManager": "bun@1.4.4"` with the exact package versions specified in ADR-0016 (no caret `^` or tilde `~` ranges):
  - `svelte`: 5.57.3
  - `@sveltejs/kit`: 3.2.0
  - `@sveltejs/adapter-static`: 4.0.1
  - `@sveltejs/vite-plugin-svelte`: 7.3.1
  - `vite`: 8.3.7
  - `tailwindcss`: 4.3.5
  - `@tailwindcss/vite`: 4.3.5
  - `bits-ui`: 2.20.6
  - `@internationalized/date`: 3.12.4
  - `typescript`: 6.0.3
  - `svelte-check`: 4.7.8
- Create `ui/bun.lock` (text lockfile generated via Bun 1.4.4 with exact pins).
- Create `ui/vite.config.ts` configuring `@sveltejs/kit/vite` and `@tailwindcss/vite`, with the SvelteKit options (including the adapter) passed to `sveltekit(...)`:
  `sveltekit({ adapter: adapter({ pages: '../internal/ui/dist', assets: '../internal/ui/dist', fallback: 'index.html' }) })`, where `adapter` is `@sveltejs/adapter-static`, giving static pages and assets in `../internal/ui/dist` with `fallback: 'index.html'` for client-side routing. Note: `ui/svelte.config.js` must NOT exist; SvelteKit 3.2 rejects it.
- Create `ui/tsconfig.json` extending `./.svelte-kit/tsconfig.json` with strict type checking.
- Create `ui/src/app.html` with basic HTML structure, viewport meta, and SvelteKit `%sveltekit.head%` and `%sveltekit.body%` placeholders.
- Create root `ui/src/routes/+layout.svelte` with `{@render children()}` (Svelte 5 snippet syntax).
- Create `ui/.gitignore` ignoring `node_modules`, `.svelte-kit`, build artifacts, and local editor files. Neither `ui/node_modules` nor `internal/ui/dist` may be committed.
- Create a test-only Go package at `./ui` (e.g. `ui/scaffold_test.go`) run by `go test` that inspects the `ui/` directory and asserts the configuration directly without requiring a browser in the Go test runner.

## Acceptance criteria

- `TestScaffoldFilesPresent`: asserts that all required project files (`package.json`, `bun.lock`, `vite.config.ts`, `tsconfig.json`, `src/app.html`, `src/routes/+layout.svelte`, `.gitignore`) exist and are non-empty, and asserts that `ui/svelte.config.js` does NOT exist.
- `TestPackageJSONExactPins`: parses `ui/package.json`, asserts `"packageManager"` is `"bun@1.4.4"`, and verifies that every dependency listed matches the exact version in ADR-0016 without any `^` or `~` prefix.
- `TestViteConfigAdapterStatic`: reads `ui/vite.config.ts` and asserts that `@sveltejs/adapter-static` is imported and configured to output to `../internal/ui/dist`.
- `TestViteConfigTailwind`: verifies that `ui/vite.config.ts` configures both SvelteKit and `@tailwindcss/vite`.
- `go test ./ui` passes.
- `make ci` stays green with or without `ui/node_modules` present.

## Out of scope

- Tokens, fonts, SVG assets, or CSS styling (handled in W2-Sc).
- Shell layout components, sidebar, and views (handled in W2-Sa and W2-S).
- Modifying `Makefile`, root `.gitignore`, or `.github/workflows/ci.yml` (handled in W2-S).

## Security notes

- Strict dependency pinning prevents pulling unvetted or compromised package versions.
- All packages and tools run through Bun 1.4.4 with a frozen lockfile.
- No network requests are made during build verification.
