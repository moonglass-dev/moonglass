package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/policy"
)

func TestPolicyEnvReadsEngine(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)

	env, err := PolicyEnv(ctx, e)
	if err != nil {
		t.Fatalf("PolicyEnv: unexpected error %v", err)
	}
	info, err := e.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatalf("Engine Info: unexpected error %v", err)
	}

	requireEqual(t, "Paths.MountsRoot", env.Paths.MountsRoot, "/var/lib/moonglass/mounts")
	requireEqual(t, "Paths.DockerSockets", env.Paths.DockerSockets,
		[]string{"/var/run/docker.sock", "/run/docker.sock"})
	requireEqual(t, "Paths.ContainerdSockets", env.Paths.ContainerdSockets,
		[]string{"/run/containerd/containerd.sock"})
	requireEqual(t, "Paths.DockerRoot", env.Paths.DockerRoot, info.Info.DockerRootDir)
	requireEqual(t, "DefaultRuntime", env.DefaultRuntime, info.Info.DefaultRuntime)
	if env.ContainerProject == nil {
		t.Fatalf("ContainerProject = nil, want the label lookup")
	}

	ref := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	pol := emptyPolicy(t, env)

	labelled := fixtureSpec(p, "app-labels", "r1", ref, true)
	labelledID := createFixture(t, e, pol, labelled, "mg-"+p.name+"-labels")
	project, ok := env.ContainerProject(labelledID)
	if project != p.name || !ok {
		t.Errorf("ContainerProject(labelled container) = (%q, %v), want (%q, true)", project, ok, p.name)
	}

	// A managed container that carries no project label is unknown, so it counts
	// as cross-project; it is removed by its own cleanup.
	unlabelled := policy.Spec{
		Kind: "app", Project: p.name, Environment: testEnvironment, Service: testService, Image: ref,
		AgentLabels: map[string]string{testLabelManaged: "true"},
	}
	unlabelledID := createFixture(t, e, pol, unlabelled, "mg-"+p.name+"-unlabelled")
	t.Cleanup(func() {
		_, _ = e.cli.ContainerRemove(context.Background(), unlabelledID,
			client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})
	project, ok = env.ContainerProject(unlabelledID)
	if project != "" || ok {
		t.Errorf("ContainerProject(unlabelled container) = (%q, %v), want (\"\", false)", project, ok)
	}

	project, ok = env.ContainerProject("mg-missing-" + p.name)
	if project != "" || ok {
		t.Errorf("ContainerProject(missing container) = (%q, %v), want (\"\", false)", project, ok)
	}

	t.Run("info failure names the Engine", func(t *testing.T) {
		dead, err := client.New(client.WithHost("unix:///nonexistent/moonglass-test.sock"))
		if err != nil {
			t.Fatalf("client.New: unexpected error %v", err)
		}
		defer func() { _ = dead.Close() }()
		if _, err := PolicyEnv(ctx, &Engine{cli: dead}); err == nil {
			t.Fatalf("PolicyEnv over a dead Engine: error = nil, want one naming the Docker Engine")
		} else if !strings.Contains(err.Error(), "Docker Engine") {
			t.Errorf("PolicyEnv over a dead Engine: error = %q, want it to name the Docker Engine", err)
		}
	})
}
