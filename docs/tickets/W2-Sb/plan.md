---
verdict: ready
reason: ""
---

# Plan for W2-Sb

ADR-0016 (amended 2026-11-19) settles every choice: Bun 1.4.4, the eleven exact pins, kit options and the adapter passed to `sveltekit(...)` in `ui/vite.config.ts`, no `ui/svelte.config.js`, output to `../internal/ui/dist`. No contract, ADR, module or security decision is needed. `ui/` does not exist yet; everything below is new.

## Files to change

- `ui/package.json`: `"name": "moonglass-ui"`, `"private": true`, `"type": "module"`, `"packageManager": "bun@1.4.4"`. Scripts: `"dev": "vite dev"`, `"build": "vite build"`, `"check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json"`. All eleven ADR-0016 packages go in `devDependencies` (the output is static; nothing ships as a runtime dependency) at exactly: `svelte` 5.57.3, `@sveltejs/kit` 3.2.0, `@sveltejs/adapter-static` 4.0.1, `@sveltejs/vite-plugin-svelte` 7.3.1, `vite` 8.3.7, `tailwindcss` 4.3.5, `@tailwindcss/vite` 4.3.5, `bits-ui` 2.20.6, `@internationalized/date` 3.12.4, `typescript` 6.0.3, `svelte-check` 4.7.8. No other package, no `dependencies` key.
- `ui/bun.lock`: generated, never hand-edited. Run `bun install` in `ui/` with Bun 1.4.4, then `bun install --frozen-lockfile` to confirm it is stable. Text format (Bun 1.4 default).
- `ui/vite.config.ts`:
  ```ts
  import adapter from '@sveltejs/adapter-static';
  import { sveltekit } from '@sveltejs/kit/vite';
  import tailwindcss from '@tailwindcss/vite';
  import { defineConfig } from 'vite';

  export default defineConfig({
  	plugins: [
  		tailwindcss(),
  		sveltekit({
  			adapter: adapter({ pages: '../internal/ui/dist', assets: '../internal/ui/dist', fallback: 'index.html' })
  		})
  	]
  });
  ```
  `fallback` is adapter-static's option name; `tools/pipeline/verify.go` `bannedWordsSkip` already exempts this file.
- `ui/tsconfig.json`: `"extends": "./.svelte-kit/tsconfig.json"`, `compilerOptions`: `strict: true`, `moduleResolution: "bundler"`, `allowJs: true`, `checkJs: true`, `esModuleInterop: true`, `forceConsistentCasingInFileNames: true`, `resolveJsonModule: true`, `skipLibCheck: true`, `sourceMap: true`.
- `ui/src/app.html`: `<!doctype html>`, `<html lang="en">`, `<meta charset="utf-8" />`, `<meta name="viewport" content="width=device-width, initial-scale=1" />`, `%sveltekit.head%` in `<head>`, and `<body data-sveltekit-preload-data="hover"><div>%sveltekit.body%</div></body>`. No inline `style` or `<script>`, so a strict CSP (`docs/specs/ui.md` §11) stays possible.
- `ui/src/routes/+layout.svelte`:
  ```svelte
  <script lang="ts">
  	import type { Snippet } from 'svelte';

  	let { children }: { children: Snippet } = $props();
  </script>

  {@render children()}
  ```
- `ui/.gitignore`: `/node_modules`, `/.svelte-kit`, `/build`, `.DS_Store`, `/.vscode`, `/.idea`, `*.log`, `vite.config.ts.timestamp-*`. It cannot cover `internal/ui/dist` (outside `ui/`); the root `.gitignore` entry belongs to W2-S, so this ticket never runs `vite build` in the worktree (see Risks).
- `ui/scaffold_test.go`: `package ui`, standard library only (`encoding/json`, `errors`, `io/fs`, `os`, `strings`, `testing`). Paths are relative to the package directory, which is `go test`'s working directory. Helper `readFile(t *testing.T, name string) string` that fails with the path and error.
- `testdata/tests.txt`: `make test-list` adds the four `moonglass.dev/moonglass/ui` lines.

## Tests to write

All in `./ui`, table-driven, failures print want and got.

- `TestScaffoldFilesPresent`: table of `package.json`, `bun.lock`, `vite.config.ts`, `tsconfig.json`, `src/app.html`, `src/routes/+layout.svelte`, `.gitignore`; each `os.Stat` succeeds, is a regular file, and has size > 0. Then `os.Stat("svelte.config.js")` must return `errors.Is(err, fs.ErrNotExist)`.
- `TestPackageJSONExactPins`: unmarshal into `struct { PackageManager string; Dependencies, DevDependencies map[string]string }` (JSON tags). Assert `PackageManager == "bun@1.4.4"`. Merge both maps (fail if a name is in both); the merged set must equal the ADR-0016 table exactly: every wanted package present at the exact version, no extra package (ADR-0016: adding one is an ADR change). Each version is also checked for a leading `^` or `~` so the message names the range. Then each `"<name>@<version>"` must appear in `bun.lock`, so the lockfile resolves the pinned versions.
- `TestViteConfigAdapterStatic`: `vite.config.ts` contains each of: `import adapter from '@sveltejs/adapter-static'`, `adapter: adapter(`, `pages: '../internal/ui/dist'`, `assets: '../internal/ui/dist'`, `fallback: 'index.html'`.
- `TestViteConfigTailwind`: `vite.config.ts` contains each of: `import { sveltekit } from '@sveltejs/kit/vite'`, `import tailwindcss from '@tailwindcss/vite'`, `tailwindcss()`, `sveltekit(`, `plugins:`.

Write the test file first and confirm all four fail on the empty `ui/` (`make test-one PKG=./ui NAME=...`) before adding the scaffold.

## Edge cases

- Caret or tilde range, wrong version, missing package, extra package, package in both maps: each fails `TestPackageJSONExactPins` with the package named.
- Lockfile resolving a different version than `package.json`: fails the `bun.lock` substring check in `TestPackageJSONExactPins`.
- `svelte.config.js` reappearing (SvelteKit 3.2 rejects it): fails `TestScaffoldFilesPresent`.
- An empty placeholder file: fails the size check in `TestScaffoldFilesPresent`.
- Adapter pointed elsewhere or `fallback` dropped: fails `TestViteConfigAdapterStatic`; Tailwind plugin dropped: fails `TestViteConfigTailwind`.
- No `ui/node_modules`: none of the tests read it, so `go test ./ui` passes on a clean clone.

## Risks

- **Size cap (400 counted lines).** `ui/bun.lock` counts toward size (`countsTowardSize` in `tools/pipeline/verify.go` does not exempt it). Bun's text lockfile is one line per package; the tree (Kit, Vite 8, Tailwind oxide and lightningcss platform binaries, bits-ui) should be about 150–220 lines, plus about 110 for the rest. Keep every hand-written file minimal. If the generated lockfile pushes the total over 400, write `BLOCKED.md`: the lockfile cannot be trimmed by hand.
- **`node_modules` and `go ./...`.** Go does not skip `node_modules`. If any installed package ships `.go` files without its own `go.mod`, `go test ./...`, `go vet` and `make test-list` would pick them up and `make ci` would fail with `node_modules` present. After `bun install`, run `go list ./... | grep node_modules` and run `make ci` with `ui/node_modules` present and again after deleting it. If Go sees a package, block: the fix (an `ignore ./ui/node_modules` directive) is in `go.mod`, a protected path.
- **Dirty worktree.** `internal/ui/dist` is not git-ignored until W2-S. Do not run `vite build` or `make ui` in the worktree. If a build is run to check the config, delete `internal/ui/dist` afterwards so the `clean` gate passes. Commit neither `ui/node_modules` nor `ui/.svelte-kit`.
- **Banned words.** Only `ui/vite.config.ts` may contain "fallback". Check `ui/bun.lock` with `grep -inwE 'todo|fixme|xxx|hack|fallback|legacy' ui/bun.lock` (an integrity hash can match by chance). If it matches, block rather than edit the lockfile.
- **Lint and format.** `ui/scaffold_test.go` must pass gofumpt, goimports and golangci-lint: wrap errors with context, no unchecked errors. `make fmt` before `make ci`.
- **Test list.** Run `make test-list` after adding the tests so `check-test-list` passes.
- **No later-ticket code.** No CSS, tokens, fonts, `+page.svelte`, `app.d.ts`, `internal/ui` Go package, Makefile or CI changes. Those belong to W2-Sc, W2-Sa and W2-S.
