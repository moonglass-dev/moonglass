package policy

import (
	"context"
	"strings"
	"testing"
)

func TestCheckRejectsAbsolutePathDisguisedAsVolume(t *testing.T) {
	l := newLayout(t)
	p := mustParse(t, l.Env, "")
	s := baseSpec()
	s.Mounts = []Mount{{Type: "volume", Source: l.Sock, Target: "/x"}}
	if _, err := p.Check(s); !hasKey(err, "docker_socket") {
		t.Fatalf("Check(volume source of docker.sock) error = %v, want a docker_socket rejection", err)
	}
}

func TestCheckComposeRejectsVolumesFrom(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  web:
    image: `+imgA+`
    volumes_from:
      - container:other1
`)
	p := mustParse(t, l.Env, "")
	_, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	if err == nil || !hasField(err, "volumes_from") {
		t.Fatalf("CheckCompose(volumes_from) error = %v, want a volumes_from rejection", err)
	}
}

func TestCheckComposeBuildNetworkHostIsEntitlement(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  b:
    build:
      context: .
      network: host
`)
	p := mustParse(t, l.Env, "")
	_, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	if !hasKey(err, "build_entitlements") {
		t.Fatalf("CheckCompose(build.network: host) error = %v, want a build_entitlements rejection", err)
	}
}

func TestCheckComposeNamedVolumeOverlayOptsAreBinds(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  web:
    image: `+imgA+`
    volumes: ["sock:/x"]
volumes:
  sock:
    driver: local
    driver_opts:
      type: overlay
      device: overlay
      o: "lowerdir=`+l.SockDir+`,upperdir=/tmp/u,workdir=/tmp/w"
`)
	p := mustParse(t, l.Env, "")
	_, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	if err == nil {
		t.Fatal("CheckCompose(overlay lowerdir of the docker sock dir) error = nil, want a rejection")
	}
	if !hasKey(err, "docker_socket") && !strings.Contains(err.Error(), "docker") && !hasKey(err, "bind_mounts") {
		// lowerdir is the socket directory (parent), classified as protected docker_socket
		if !hasKey(err, "docker_socket") && !hasKey(err, "bind_mounts") {
			t.Fatalf("CheckCompose(overlay) error = %v keys %v, want docker_socket or bind_mounts", err, keysOf(err))
		}
	}
}
