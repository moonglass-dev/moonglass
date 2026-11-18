package deploy

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

func (e *Engine) ensureNetwork(ctx context.Context, project string) error {
	name := "mg-" + project
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		if !isNotFound(err) {
			return fmt.Errorf("inspect network %s: %w", name, err)
		}
		_, err := e.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
			Driver: "bridge",
			Labels: map[string]string{labelManaged: "true", labelProject: project},
		})
		if err != nil {
			return fmt.Errorf("create network %s: %w", name, err)
		}
		return nil
	}
	if res.Network.Name != name || res.Network.Labels[labelManaged] != "true" || res.Network.Labels[labelProject] != project {
		return fmt.Errorf("network %s exists and is not managed by Moonglass", name)
	}
	return nil
}
