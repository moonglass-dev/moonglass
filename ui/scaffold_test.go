// Package ui holds the Go tests for the SvelteKit application in this
// directory. ADR-0016 fixes the package manager, the exact pins, the static
// adapter output and the Vite plugins; these tests read the scaffold files
// directly, so no browser or Node toolchain is needed to verify them.
package ui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// adrPins is the exact dependency set from ADR-0016 decision 2. Adding or
// upgrading a package is a change to that ADR, so the set is closed.
var adrPins = map[string]string{
	"svelte":                       "5.57.3",
	"@sveltejs/kit":                "3.2.0",
	"@sveltejs/adapter-static":     "4.0.1",
	"@sveltejs/vite-plugin-svelte": "7.3.1",
	"vite":                         "8.3.7",
	"tailwindcss":                  "4.3.5",
	"@tailwindcss/vite":            "4.3.5",
	"bits-ui":                      "2.20.6",
	"@internationalized/date":      "3.12.4",
	"typescript":                   "6.0.3",
	"svelte-check":                 "4.7.8",
}

// readFile returns the contents of name, which is relative to this package
// directory (go test's working directory), and fails the test when it cannot.
func readFile(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v (the SvelteKit scaffold in ui/ must provide it)", name, err)
	}

	return string(data)
}

func TestScaffoldFilesPresent(t *testing.T) {
	for _, name := range []string{
		"package.json",
		"bun.lock",
		"vite.config.ts",
		"tsconfig.json",
		"src/app.html",
		"src/routes/+layout.svelte",
		".gitignore",
	} {
		t.Run(name, func(t *testing.T) {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatalf("stat %s: %v, want an existing file (ADR-0016)", name, err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("%s mode = %v, want a regular file", name, info.Mode())
			}
			if info.Size() == 0 {
				t.Fatalf("%s size = 0, want a non-empty file", name)
			}
		})
	}

	if _, err := os.Stat("svelte.config.js"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat svelte.config.js: err = %v, want %v (SvelteKit 3.2 takes its options from vite.config.ts and rejects svelte.config.js)", err, fs.ErrNotExist)
	}
}

func TestPackageJSONExactPins(t *testing.T) {
	var pkg struct {
		PackageManager  string            `json:"packageManager"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(readFile(t, "package.json")), &pkg); err != nil {
		t.Fatalf("parse package.json: %v", err)
	}

	if want := "bun@1.4.4"; pkg.PackageManager != want {
		t.Errorf("packageManager = %q, want %q (ADR-0016 decision 1)", pkg.PackageManager, want)
	}

	got := make(map[string]string, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for name, version := range pkg.Dependencies {
		got[name] = version
	}
	for name, version := range pkg.DevDependencies {
		if _, duplicate := got[name]; duplicate {
			t.Errorf("%s is in both dependencies and devDependencies (versions %q and %q), want exactly one entry", name, got[name], version)
			continue
		}
		got[name] = version
	}

	for name, want := range adrPins {
		version, ok := got[name]
		if !ok {
			t.Errorf("missing dependency %s, want %q (ADR-0016)", name, want)
			continue
		}
		if strings.HasPrefix(version, "^") || strings.HasPrefix(version, "~") {
			t.Errorf("%s version = %q, want the exact pin %q without a ^ or ~ range", name, version, want)
			continue
		}
		if version != want {
			t.Errorf("%s version = %q, want %q (ADR-0016)", name, version, want)
		}
	}

	for name, version := range got {
		if _, ok := adrPins[name]; !ok {
			t.Errorf("unexpected dependency %s at %q, want only the ADR-0016 set", name, version)
		}
	}

	lock := readFile(t, "bun.lock")
	for name, version := range adrPins {
		want := name + "@" + version
		if !strings.Contains(lock, want) {
			t.Errorf("bun.lock does not contain %q, want the lockfile to resolve the pinned version", want)
		}
	}
}

func TestViteConfigAdapterStatic(t *testing.T) {
	config := readFile(t, "vite.config.ts")

	for _, want := range []string{
		"import adapter from '@sveltejs/adapter-static'",
		"adapter: adapter(",
		"pages: '../internal/ui/dist'",
		"assets: '../internal/ui/dist'",
		"fallback: 'index.html'",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("vite.config.ts is missing %q, want @sveltejs/adapter-static to emit pages and assets to ../internal/ui/dist with fallback: 'index.html'", want)
		}
	}
}

func TestViteConfigTailwind(t *testing.T) {
	config := readFile(t, "vite.config.ts")

	for _, want := range []string{
		"import { sveltekit } from '@sveltejs/kit/vite'",
		"import tailwindcss from '@tailwindcss/vite'",
		"tailwindcss()",
		"sveltekit(",
		"plugins:",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("vite.config.ts is missing %q, want both the SvelteKit and Tailwind plugins registered", want)
		}
	}
}
