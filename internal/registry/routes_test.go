package registry_test

import (
	"testing"

	"moonglass.dev/moonglass/internal/registry"
)

func TestAgentConnectSynopsisNamesProxyFlags(t *testing.T) {
	const want = "agent connect [--data-dir DIR] [--policy FILE] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL"
	for _, c := range registry.LocalCommands {
		if c.Name != "agent connect" {
			continue
		}
		if c.Synopsis != want {
			t.Errorf("agent connect Synopsis = %q, want %q", c.Synopsis, want)
		}
		return
	}
	t.Fatalf("registry.LocalCommands has no %q entry, want one with Synopsis %q", "agent connect", want)
}

func TestAgentConnectSynopsisNamesPolicyFlag(t *testing.T) {
	const want = "agent connect [--data-dir DIR] [--policy FILE] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL"
	for _, c := range registry.LocalCommands {
		if c.Name != "agent connect" {
			continue
		}
		if c.Synopsis != want {
			t.Errorf("agent connect Synopsis = %q, want %q", c.Synopsis, want)
		}
		return
	}
	t.Fatalf("registry.LocalCommands has no %q entry, want one with Synopsis %q", "agent connect", want)
}
