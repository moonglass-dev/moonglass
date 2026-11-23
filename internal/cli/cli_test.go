package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/registry"
)

func localHandlers() map[string]func([]string) int {
	m := map[string]func([]string) int{}
	for _, c := range registry.LocalCommands {
		m[c.Name] = func([]string) int { return 0 }
	}
	return m
}

func TestEverySubcommandIsGeneratedOrLocal(t *testing.T) {
	var want []string
	for _, s := range registry.Operations() {
		want = append(want, strings.ReplaceAll(s.Name, ".", " "))
	}
	localNames := map[string]bool{}
	for _, c := range registry.LocalCommands {
		want = append(want, c.Name)
		localNames[c.Name] = true
		if c.Touches == "" {
			t.Errorf("local command %q has an empty Touches, want what it touches", c.Name)
		}
	}
	for _, name := range []string{"version", "server", "agent enroll", "agent connect", "proxy"} {
		if !localNames[name] {
			t.Errorf("registry.LocalCommands lacks %q, want it listed", name)
		}
	}
	got := cli.Subcommands()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cli.Subcommands() = %v, want generated operations plus local commands %v", got, want)
	}

	var out, errb bytes.Buffer
	env := func(string) string { return "" }
	if code := cli.Run([]string{"no-such-command"}, env, &out, &errb, localHandlers()); code != 2 {
		t.Errorf("Run(unknown subcommand) exit = %d, want 2", code)
	}
	for name, local := range map[string]map[string]func([]string) int{
		"missing key": {"version": func([]string) int { return 0 }},
		"extra key": func() map[string]func([]string) int {
			m := localHandlers()
			m["rogue"] = func([]string) int { return 0 }
			return m
		}(),
	} {
		if code := cli.Run([]string{"version"}, env, &out, &errb, local); code != 2 {
			t.Errorf("Run with local handlers (%s) exit = %d, want 2", name, code)
		}
	}
	called := false
	local := localHandlers()
	local["version"] = func([]string) int { called = true; return 0 }
	if code := cli.Run([]string{"version"}, env, &out, &errb, local); code != 0 || !called {
		t.Errorf("Run(version) exit = %d, handler called = %v, want 0 and true", code, called)
	}
}

func TestGeneratedRunnerSendsBearerToken(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	defer ts.Close()
	env := func(k string) string {
		return map[string]string{"MOONGLASS_TOKEN": "mg_secret", "MOONGLASS_URL": ts.URL}[k]
	}
	var out, errb bytes.Buffer
	if code := cli.Run([]string{"servers", "list"}, env, &out, &errb, localHandlers()); code != 0 {
		t.Fatalf("Run(servers list) exit = %d, stderr = %q, want 0", code, errb.String())
	}
	if gotAuth != "Bearer mg_secret" || gotPath != "/v1/servers" || gotMethod != "GET" {
		t.Errorf("request = %s %s auth %q, want GET /v1/servers auth %q", gotMethod, gotPath, gotAuth, "Bearer mg_secret")
	}
	if !strings.Contains(out.String(), `"servers"`) {
		t.Errorf("stdout = %q, want the response body", out.String())
	}
}
