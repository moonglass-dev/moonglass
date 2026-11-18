package deploy

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/policy"
)

func createOptions(c policy.Checked, name string) (client.ContainerCreateOptions, error) {
	s := c.Spec()
	if err := uncarried(s); err != nil {
		return client.ContainerCreateOptions{}, err
	}
	env := make([]string, 0, len(s.Env))
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, k+"="+s.Env[k])
	}
	labels := make(map[string]string, len(s.Labels)+len(s.AgentLabels))
	maps.Copy(labels, s.Labels)
	maps.Copy(labels, s.AgentLabels)
	endpoints := make(map[string]*network.EndpointSettings, len(s.Networks))
	for _, n := range s.Networks {
		endpoints[n] = &network.EndpointSettings{}
	}
	mode := container.NetworkMode("")
	if len(s.Networks) > 0 {
		mode = container.NetworkMode(s.Networks[0])
	}
	return client.ContainerCreateOptions{
		Name:   name,
		Config: &container.Config{Image: s.Image, Cmd: s.Cmd, Env: env, Labels: labels},
		HostConfig: &container.HostConfig{
			NetworkMode:   mode,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(s.RestartPolicy)},
			LogConfig:     container.LogConfig{Type: s.LogConfig.Driver, Config: s.LogConfig.Options},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: endpoints},
	}, nil
}

func uncarried(s policy.Spec) error {
	v := reflect.ValueOf(s)
	for _, name := range strings.Fields("Privileged Mounts NetworkMode PID IPC UTS UserNS CapAdd Devices" +
		" DeviceCgroupRules SecurityOpt Sysctls CgroupParent Runtime BuildEntitlements") {
		f := v.FieldByName(name)
		if !f.IsZero() {
			return fmt.Errorf("container spec field %s is set, but the executor does not send it to the Docker Engine; nothing was created", name)
		}
	}
	return nil
}

func (e *Engine) create(ctx context.Context, c policy.Checked, name string) (string, error) {
	opts, err := createOptions(c, name)
	if err != nil {
		return "", err
	}
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ContainerCreate(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", name, err)
	}
	return res.ID, nil
}

func (e *Engine) clearName(ctx context.Context, name, appID, releaseID string) error {
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect container %s: %w", name, err)
	}
	if res.Container.Name != "/"+name {
		return nil
	}
	if res.Container.Config.Labels[labelManaged] == "true" && res.Container.Config.Labels[labelApp] == appID && res.Container.Config.Labels[labelRelease] == releaseID {
		if _, err := e.cli.ContainerRemove(ctx, res.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove leftover container %s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("container %s exists and is not a Moonglass container of release %s; remove or rename it", name, releaseID)
}

func (e *Engine) start(ctx context.Context, id string) error {
	ctx, cancel := call(ctx)
	defer cancel()
	if _, err := e.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect container %s after start: %w", id, err)
	}
	return runningAfterStart(res.Container.State)
}

func runningAfterStart(s *container.State) error {
	if s.Running && !s.Restarting {
		return nil
	}
	return fmt.Errorf("container exited right after start (status %s, exit code %d); see its logs", s.Status, s.ExitCode)
}

func (e *Engine) remove(ctx context.Context, id string) error {
	ctx, cancel := call(context.WithoutCancel(ctx))
	defer cancel()
	if _, err := e.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}
