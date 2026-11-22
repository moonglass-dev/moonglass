package deploy

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/policy"
)

// PolicyEnv reads the Engine once, so agent.toml names this host (container-policy.md, "API").
func PolicyEnv(ctx context.Context, e *Engine) (policy.Env, error) {
	callCtx, cancel := call(ctx)
	defer cancel()
	info, err := e.cli.Info(callCtx, client.InfoOptions{})
	if err != nil {
		return policy.Env{}, fmt.Errorf("read Docker Engine info for agent.toml: %w", err)
	}
	return policy.Env{
		Paths: policy.Paths{
			MountsRoot:        "/var/lib/moonglass/mounts",
			DockerRoot:        info.Info.DockerRootDir,
			DockerSockets:     []string{"/var/run/docker.sock", "/run/docker.sock"},
			ContainerdSockets: []string{"/run/containerd/containerd.sock"},
		},
		DefaultRuntime: info.Info.DefaultRuntime,
		ContainerProject: func(ref string) (string, bool) {
			return e.containerProject(ctx, ref)
		},
	}, nil
}

// containerProject reads the project label; an unknown container is cross-project.
func (e *Engine) containerProject(ctx context.Context, ref string) (string, bool) {
	callCtx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ContainerInspect(callCtx, ref, client.ContainerInspectOptions{})
	if err != nil || res.Container.Config == nil {
		return "", false
	}
	project := res.Container.Config.Labels[labelProject]
	if project == "" {
		return "", false
	}
	return project, true
}
