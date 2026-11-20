package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// registryRef is registry:2 pinned by the digest Docker Hub serves for it
// (docker buildx imagetools inspect registry:2, 2026-11-18). It is the only
// image these tests pull (docs/specs/deploy-suite.md).
const registryRef = "registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"

func TestDeployResolvesRegistryImages(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	registryID, port := startRegistry(t, e, p)

	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	repo := "localhost:" + port + "/" + p.name + "/web"
	tag := repo + ":v1"
	p.registerImage(tag)

	imageA := "moonglass.local/" + p.name + "/web:a"
	p.registerImage(imageA)
	imageAID := importImage(t, e, imageA, []string{"/app"})
	if _, err := e.cli.ImageTag(ctx, client.ImageTagOptions{Source: imageAID, Target: tag}); err != nil {
		t.Fatalf("ImageTag(%s -> %s) error = %v, want nil", imageAID, tag, err)
	}
	digest := pushTag(t, e, tag)
	digestRef := repo + "@" + digest

	// A different local image now holds the tag, so a deploy of the tag must
	// pull the registry's image and not run the stale local one.
	imageB := "moonglass.local/" + p.name + "/web:b"
	p.registerImage(imageB)
	imageBID := importImage(t, e, imageB, []string{"/app", "b"})
	if _, err := e.cli.ImageTag(ctx, client.ImageTagOptions{Source: imageBID, Target: tag}); err != nil {
		t.Fatalf("ImageTag(%s -> %s) error = %v, want nil", imageBID, tag, err)
	}
	if imageAID == imageBID {
		t.Fatalf("fixture image IDs = both %s, want two different images", imageAID)
	}

	res := x.Deploy(ctx, requestID(t), deployMsg(p.name, appA, "r1", tag))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(tag) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireEqual(t, "repo_digest", res.GetRepoDigest(), digestRef)
	pulled := inspectImage(t, e, digestRef).ID
	requireEqual(t, "image_id", res.GetImageId(), pulled)
	if res.GetImageId() == imageBID {
		t.Errorf("image_id = %s, the stale local image with that tag, want the pulled image %s",
			res.GetImageId(), pulled)
	}
	row, found := storedRow(t, dir, appA, "r1")
	if !found {
		t.Fatalf("releases row for appA/r1 = missing, want status candidate")
	}
	requireEqual(t, "row repo_digest", row.RepoDigest, digestRef)
	requireEqual(t, "row image_id", row.ImageID, res.GetImageId())
	c := requireContainer(t, e, testContainerName(appA, "r1"))
	requireEqual(t, "container image", c.Image, res.GetImageId())

	// With the registry gone, the digest reference still resolves from the
	// local store, so nothing is pulled.
	if _, err := e.cli.ContainerRemove(ctx, registryID, client.ContainerRemoveOptions{Force: true}); err != nil {
		t.Fatalf("ContainerRemove(registry %s) error = %v, want nil", registryID, err)
	}
	res = x.Deploy(ctx, requestID(t), deployMsg(p.name, appB, "r2", digestRef))
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(digest, registry stopped) outcome = %v with detail %q, want %v",
			got, res.GetDetail(), want)
	}
	requireEqual(t, "repo_digest with the registry stopped", res.GetRepoDigest(), digestRef)
	requireEqual(t, "image_id with the registry stopped", res.GetImageId(), pulled)
	requireRunning(t, e, testContainerName(appB, "r2"))
}

// startRegistry pulls and runs the pinned registry:2 on a random localhost
// port. It returns the container ID and the port.
func startRegistry(t *testing.T, e *Engine, p *project) (string, string) {
	t.Helper()
	ctx := context.Background()

	pull, err := e.cli.ImagePull(ctx, registryRef, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("ImagePull(%q) error = %v, want nil", registryRef, err)
	}
	if err := pull.Wait(ctx); err != nil {
		_ = pull.Close()
		t.Fatalf("ImagePull(%q) error = %v, want nil", registryRef, err)
	}
	if err := pull.Close(); err != nil {
		t.Fatalf("ImagePull(%q) close: %v", registryRef, err)
	}

	const containerPort = "5000/tcp"
	name := "mg-" + p.name + "-registry"
	created, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:        registryRef,
			ExposedPorts: network.PortSet{network.MustParsePort(containerPort): {}},
			Labels:       map[string]string{testLabelProject: p.name},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{
				network.MustParsePort(containerPort): {{HostIP: netip.MustParseAddr("127.0.0.1")}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ContainerCreate(%q) error = %v, want nil", name, err)
	}
	if _, err := e.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("ContainerStart(%q) error = %v, want nil", name, err)
	}
	inspected, err := e.cli.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect(%q) error = %v, want nil", name, err)
	}
	if inspected.Container.NetworkSettings == nil {
		t.Fatalf("registry %s NetworkSettings = nil, want its published port", name)
	}
	bindings := inspected.Container.NetworkSettings.Ports[network.MustParsePort(containerPort)]
	if len(bindings) == 0 || bindings[0].HostPort == "" {
		t.Fatalf("registry %s published ports = %#v, want 5000/tcp bound to a host port",
			name, inspected.Container.NetworkSettings.Ports)
	}
	port := bindings[0].HostPort
	waitForRegistry(t, port)
	return created.ID, port
}

// pushTag pushes ref and returns the digest the registry serves for it.
func pushTag(t *testing.T, e *Engine, ref string) string {
	t.Helper()
	ctx := context.Background()
	push, err := e.cli.ImagePush(ctx, ref, client.ImagePushOptions{
		RegistryAuth: base64.StdEncoding.EncodeToString([]byte("{}")),
	})
	if err != nil {
		t.Fatalf("ImagePush(%q) error = %v, want nil", ref, err)
	}
	defer func() { _ = push.Close() }()
	for msg, err := range push.JSONMessages(ctx) {
		if err != nil {
			t.Fatalf("ImagePush(%q): %v", ref, err)
		}
		if msg.Error != nil {
			t.Fatalf("ImagePush(%q) error = %s, want nil", ref, msg.Error.Message)
		}
		if msg.Aux != nil {
			var aux struct{ Digest string }
			if err := json.Unmarshal(*msg.Aux, &aux); err != nil {
				continue
			}
			if strings.HasPrefix(aux.Digest, "sha256:") {
				return aux.Digest
			}
		}
		if i := strings.Index(msg.Status, "digest: "); i >= 0 {
			fields := strings.Fields(msg.Status[i+len("digest: "):])
			if len(fields) > 0 && strings.HasPrefix(fields[0], "sha256:") {
				return fields[0]
			}
		}
	}
	t.Fatalf("ImagePush(%q) stream = no digest in aux or status, want the pushed digest", ref)
	return ""
}

// waitForRegistry polls GET /v2/ until the registry answers, so the test never
// assumes a fixed start-up delay.
func waitForRegistry(t *testing.T, port string) {
	t.Helper()
	url := "http://127.0.0.1:" + port + "/v2/"
	httpc := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for {
		resp, err := httpc.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry on %s not ready after 60s: %v", url, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
