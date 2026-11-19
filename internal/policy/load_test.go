package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testAgentTOMLPath = "/etc/moonglass/agent.toml"

// fakeInfo is an fs.FileInfo whose mode and Sys are chosen by the test.
type fakeInfo struct {
	mode fs.FileMode
	sys  any
}

func (f fakeInfo) Name() string       { return "agent.toml" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return f.sys }

// uidOnly has the shape the deleted interface{ Uid() uint32 } assertion
// accepted, so a test proves that shape no longer reads the owner.
type uidOnly struct{ uid uint32 }

func (u uidOnly) Uid() uint32 { return u.uid }

func stat(uid int) *syscall.Stat_t {
	return &syscall.Stat_t{Uid: uint32(uid)}
}

func TestCheckAgentTOMLRefusesUnknownOwner(t *testing.T) {
	tests := []struct {
		name  string
		sys   any
		owner int
	}{
		{"nil sys, owner 0", nil, 0},
		{"nil sys, owner 1000", nil, 1000},
		{"uid method, owner 0", uidOnly{uid: 1000}, 0},
		{"uid method, matching owner", uidOnly{uid: 1000}, 1000},
		{"uid method, other owner", uidOnly{uid: 1000}, 1001},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAgentTOML(testAgentTOMLPath, fakeInfo{mode: 0o600, sys: tc.sys}, tc.owner)
			if err == nil {
				t.Fatalf("checkAgentTOML(%s, mode 0o600, Sys %T, owner %d) error = nil, want a refusal naming the path and owner", testAgentTOMLPath, tc.sys, tc.owner)
			}
			for _, want := range []string{testAgentTOMLPath, "owner"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkAgentTOML(%s, mode 0o600, Sys %T, owner %d) error = %q, want it to contain %q", testAgentTOMLPath, tc.sys, tc.owner, err.Error(), want)
				}
			}
		})
	}
}

func TestCheckAgentTOMLMode(t *testing.T) {
	const owner = 1000
	tests := []struct {
		mode    fs.FileMode
		wantErr bool
	}{
		{0o600, false},
		{0o400, false},
		{0o640, false},
		{0o644, false},
		{0o620, true},
		{0o602, true},
		{0o622, true},
		{0o666, true},
		{0o200, true},
		{0o000, true},
		{0o004, true},
		{0o044, true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%04o", tc.mode), func(t *testing.T) {
			err := checkAgentTOML(testAgentTOMLPath, fakeInfo{mode: tc.mode, sys: stat(owner)}, owner)
			if !tc.wantErr {
				if err != nil {
					t.Errorf("checkAgentTOML(%s, mode %04o, owner %d) error = %v, want nil", testAgentTOMLPath, tc.mode, owner, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkAgentTOML(%s, mode %04o, owner %d) error = nil, want a refusal naming the path and chmod", testAgentTOMLPath, tc.mode, owner)
			}
			for _, want := range []string{testAgentTOMLPath, "chmod"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkAgentTOML(%s, mode %04o, owner %d) error = %q, want it to contain %q", testAgentTOMLPath, tc.mode, owner, err.Error(), want)
				}
			}
		})
	}
}

func TestCheckAgentTOMLOwner(t *testing.T) {
	const owner = 1000
	if err := checkAgentTOML(testAgentTOMLPath, fakeInfo{mode: 0o600, sys: stat(owner)}, owner); err != nil {
		t.Errorf("checkAgentTOML(%s, uid %d, owner %d) error = %v, want nil", testAgentTOMLPath, owner, owner, err)
	}
	err := checkAgentTOML(testAgentTOMLPath, fakeInfo{mode: 0o600, sys: stat(owner + 1)}, owner)
	if err == nil {
		t.Fatalf("checkAgentTOML(%s, uid %d, owner %d) error = nil, want a refusal naming the path, chown and both uids", testAgentTOMLPath, owner+1, owner)
	}
	for _, want := range []string{testAgentTOMLPath, "chown", fmt.Sprint(owner + 1), fmt.Sprint(owner)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkAgentTOML(%s, uid %d, owner %d) error = %q, want it to contain %q", testAgentTOMLPath, owner+1, owner, err.Error(), want)
		}
	}
}

func TestCheckAgentTOMLFileType(t *testing.T) {
	const owner = 1000
	tests := []struct {
		name string
		mode fs.FileMode
		hint string
	}{
		{"symlink", fs.ModeSymlink | 0o777, "symlink"},
		{"directory", fs.ModeDir | 0o755, "not a regular file"},
		{"named pipe", fs.ModeNamedPipe | 0o600, "not a regular file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAgentTOML(testAgentTOMLPath, fakeInfo{mode: tc.mode, sys: stat(owner)}, owner)
			if err == nil {
				t.Fatalf("checkAgentTOML(%s, mode %v, owner %d) error = nil, want a refusal naming the path and %q", testAgentTOMLPath, tc.mode, owner, tc.hint)
			}
			for _, want := range []string{testAgentTOMLPath, tc.hint} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkAgentTOML(%s, mode %v, owner %d) error = %q, want it to contain %q", testAgentTOMLPath, tc.mode, owner, err.Error(), want)
				}
			}
		})
	}
}

func TestLoadFileErrorsNamePath(t *testing.T) {
	l := newLayout(t)
	uid := os.Getuid()
	writeFile := func(t *testing.T, path, content string, mode os.FileMode) {
		t.Helper()
		must(t, os.WriteFile(path, []byte(content), 0o600))
		must(t, os.Chmod(path, mode))
	}
	safe := "[update]\nchannel = \"stable\"\n"
	tests := []struct {
		name    string
		setup   func(t *testing.T) (string, int)
		enotdir bool
	}{
		{"parent is a regular file", func(t *testing.T) (string, int) {
			dir := t.TempDir()
			must(t, os.WriteFile(filepath.Join(dir, "plain"), nil, 0o600))
			return filepath.Join(dir, "plain", "agent.toml"), uid
		}, true},
		{"symlink", func(t *testing.T) (string, int) {
			dir := t.TempDir()
			target := filepath.Join(dir, "real.toml")
			writeFile(t, target, safe, 0o600)
			link := filepath.Join(dir, "agent.toml")
			must(t, os.Symlink(target, link))
			return link, uid
		}, false},
		{"directory", func(t *testing.T) (string, int) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			must(t, os.Mkdir(path, 0o755))
			return path, uid
		}, false},
		{"group writable", func(t *testing.T) (string, int) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			writeFile(t, path, safe, 0o620)
			return path, uid
		}, false},
		{"wrong owner", func(t *testing.T) (string, int) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			writeFile(t, path, safe, 0o600)
			return path, uid + 1
		}, false},
		{"invalid TOML", func(t *testing.T) (string, int) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			writeFile(t, path, "[update\n", 0o600)
			return path, uid
		}, false},
		{"unknown key", func(t *testing.T) (string, int) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			writeFile(t, path, "[update]\nbogus = 1\n", 0o600)
			return path, uid
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, owner := tc.setup(t)
			_, err := LoadFile(path, l.Env, owner)
			if err == nil {
				t.Fatalf("LoadFile(%q, owner %d) error = nil, want an error naming the path", path, owner)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("LoadFile(%q, owner %d) error = %q, want it to contain the path %q", path, owner, err.Error(), path)
			}
			if tc.enotdir && !errors.Is(err, syscall.ENOTDIR) {
				t.Errorf("LoadFile(%q, owner %d) error = %q, want errors.Is(err, syscall.ENOTDIR)", path, owner, err.Error())
			}
		})
	}

	absent := filepath.Join(t.TempDir(), "agent.toml")
	p, err := LoadFile(absent, l.Env, uid)
	if err != nil {
		t.Fatalf("LoadFile(absent %q) error = %v, want nil", absent, err)
	}
	if got, want := p.Config(), (Config{Channel: "stable"}); !reflect.DeepEqual(got, want) {
		t.Errorf("LoadFile(absent %q) Config() = %+v, want %+v", absent, got, want)
	}
}

func TestLoadFileLoadsSafeFile(t *testing.T) {
	l := newLayout(t)
	uid := os.Getuid()
	body := "[update]\nchannel = \"stable\"\n\n[sources]\nregistries = [\"ghcr.io/acme/*\"]\n"
	want := Config{Channel: "stable", Registries: []string{"ghcr.io/acme/*"}}
	for _, mode := range []os.FileMode{0o600, 0o400, 0o644} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			must(t, os.WriteFile(path, []byte(body), 0o600))
			must(t, os.Chmod(path, mode))
			p, err := LoadFile(path, l.Env, uid)
			if err != nil {
				t.Fatalf("LoadFile(%q, mode %04o) error = %v, want nil", path, mode, err)
			}
			if got := p.Config(); !reflect.DeepEqual(got, want) {
				t.Errorf("LoadFile(%q, mode %04o) Config() = %+v, want %+v", path, mode, got, want)
			}
		})
	}
}
