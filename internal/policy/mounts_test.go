package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirAllowedForNamedImageAndProject(t *testing.T) {
	l := newLayout(t)
	block := blockTOML(`data_dirs = ["db"]`)
	db := filepath.Join(l.Mounts, "monitoring", "db")
	var cases []checkCase
	for name, src := range map[string]string{
		"the directory":        db,
		"inside the directory": filepath.Join(db, "sub", "file"),
		"trailing slash":       db + "/",
	} {
		cases = append(cases, checkCase{name: name, block: block, spec: bindSpec(src, "/data", false)})
	}
	runCheckCases(t, l, cases)
}

func TestDataDirRejectsEscapes(t *testing.T) {
	l := newLayout(t)
	db := filepath.Join(l.Mounts, "monitoring", "db")
	block := blockTOML(`data_dirs = ["db"]`)

	// A mounts directory that is itself a symlink to the real one.
	mlink := filepath.Join(l.Root, "mlink")
	must(t, os.Symlink(l.Mounts, mlink))
	viaLink := l.Env
	viaLink.Paths.MountsRoot = mlink

	runCheckCases(t, l, []checkCase{
		{name: "no block", spec: bindSpec(db, "/data", false), want: []string{"data_dirs"}, msg: "data_dirs"},
		{name: "another image", block: rawBlock("monitoring", imgB, `data_dirs = ["db"]`), spec: bindSpec(db, "/data", false), want: []string{"data_dirs"}, msg: "data_dirs"},
		{name: "another name", block: blockTOML(`data_dirs = ["cache"]`), spec: bindSpec(db, "/data", false), want: []string{"data_dirs"}, msg: "data_dirs"},
		{name: "another project's subtree", block: block, spec: bindSpec(filepath.Join(l.Mounts, "other", "db"), "/data", false), want: []string{"data_dirs"}, msg: "project"},
		{name: "block for another project", block: rawBlock("other", imgA, `data_dirs = ["db"]`), spec: bindSpec(filepath.Join(l.Mounts, "other", "db"), "/data", false), want: []string{"data_dirs"}, msg: "project"},
		{name: "project directory", block: block, spec: bindSpec(filepath.Join(l.Mounts, "monitoring"), "/data", false), want: []string{"data_dirs"}, msg: "data_dirs"},
		{name: "mounts root", block: block, spec: bindSpec(l.Mounts, "/data", false), want: []string{"data_dirs"}, msg: "data_dirs"},
		{name: "agent state", block: block, spec: bindSpec(l.State, "/data", false), want: []string{"bind_mounts"}, msg: "bind_mounts"},
		{name: "symlink inside mounts", block: blockTOML(`data_dirs = ["link"]`), spec: bindSpec(filepath.Join(l.Mounts, "monitoring", "link"), "/data", false), want: []string{"-"}, msg: "symlink"},
		{name: "path through a symlink inside mounts", block: blockTOML(`data_dirs = ["link"]`), spec: bindSpec(filepath.Join(l.Mounts, "monitoring", "link", "x"), "/data", false), want: []string{"-"}, msg: "symlink"},
		{name: "symlinked mounts directory", env: &viaLink, block: block, spec: bindSpec(filepath.Join(mlink, "monitoring", "db"), "/data", false), want: []string{"-"}, msg: "symlink"},
		{name: "dot dot", block: block, spec: bindSpec(db+"/../../other/db", "/data", false), want: []string{"-"}, msg: ".."},
		{name: "dot dot back inside", block: block, spec: bindSpec(db+"/../db", "/data", false), want: []string{"-"}, msg: ".."},
	})
}

func TestSocketAndDataRootAliasesRejected(t *testing.T) {
	l := newLayout(t)
	dockerKeys := []string{"docker_socket"}
	containerdKeys := []string{"containerd_socket"}
	rootKeys := []string{"docker_data_root"}
	anyProtected := []string{"docker_socket", "containerd_socket", "docker_data_root"}
	tests := []struct {
		name   string
		block  string
		source string
		allow  bool     // the spec must pass
		keys   []string // otherwise some violation must have one of these keys
	}{
		{"docker sock", "", l.Sock, false, dockerKeys},
		{"var/run alias", "", l.SockAlias, false, dockerKeys},
		{"symlink to the socket", "", l.SockLink, false, dockerKeys},
		{"hard link to the socket", "", l.SockHard, false, dockerKeys},
		{"dot dot path to the socket", "", l.SockDir + "/../run/docker.sock", false, []string{"-"}},
		{"parent of the socket", "", l.SockDir, false, dockerKeys},
		{"root", "", "/", false, anyProtected},
		{"checkout root", "", l.Root, false, anyProtected},
		{"containerd sock", "", l.Containerd, false, containerdKeys},
		{"parent of containerd", "", l.ContainerdDir, false, containerdKeys},
		{"docker root", "", l.Docker, false, rootKeys},
		{"inside docker root", "", l.DockerSub, false, rootKeys},

		{"docker_socket allows the socket", "docker_socket = true", l.Sock, true, nil},
		{"docker_socket allows the alias path", "docker_socket = true", l.SockAlias, true, nil},
		{"containerd_socket allows its socket", "containerd_socket = true", l.Containerd, true, nil},
		{"docker_data_root allows the root", "docker_data_root = true", l.Docker, true, nil},
		{"docker_data_root allows inside the root", "docker_data_root = true", l.DockerSub, true, nil},

		{"docker_socket never allows its parent", "docker_socket = true", l.SockDir, false, dockerKeys},
		{"docker_socket never allows root", "docker_socket = true", "/", false, anyProtected},
		{"docker_socket never allows the containerd socket", "docker_socket = true", l.Containerd, false, containerdKeys},
		{"docker_socket never allows the data root", "docker_socket = true", l.DockerSub, false, rootKeys},
		{"docker_socket never allows a dot dot path", "docker_socket = true", l.SockDir + "/../run/docker.sock", false, []string{"-"}},
		{"containerd_socket never allows its parent", "containerd_socket = true", l.ContainerdDir, false, containerdKeys},
		{"docker_data_root never allows an ancestor", "docker_data_root = true", l.Root, false, anyProtected},
		{"docker_data_root never allows the docker socket", "docker_data_root = true", l.Sock, false, dockerKeys},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			block := ""
			if tc.block != "" {
				block = blockTOML(tc.block)
			}
			p := mustParse(t, l.Env, block)
			s := bindSpec(tc.source, "/mnt/x", false)
			_, err := p.Check(s)
			if tc.allow {
				if err != nil {
					t.Fatalf("Check(bind %q) error = %v, want nil", tc.source, err)
				}
				return
			}
			if !hasKey(err, tc.keys...) {
				t.Fatalf("Check(bind %q) error = %v, want a violation with one of keys %v (got %v)", tc.source, err, tc.keys, keysOf(err))
			}
		})
	}
}
