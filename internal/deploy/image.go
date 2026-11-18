package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
)

type resolved struct{ ID, RepoDigest string }

func localProject(ref string) (string, bool, error) {
	rest, ok := strings.CutPrefix(ref, "moonglass.local/")
	if !ok {
		return "", false, nil
	}
	project, rest, ok := strings.Cut(rest, "/")
	service, release, tagged := strings.Cut(rest, ":")
	if !ok || project == "" || !tagged || service == "" || release == "" || strings.Contains(service, "/") || strings.Contains(release, ":") {
		return "", false, fmt.Errorf("image %s is not moonglass.local/<project>/<service>:<release_id>; build it again", ref)
	}
	return project, true, nil
}

func splitRef(ref string) (repo, digest string) {
	repo = ref
	if i := strings.LastIndexByte(ref, '@'); i >= 0 {
		repo, digest = ref[:i], ref[i+1:]
	}
	slash := strings.LastIndexByte(repo, '/')
	if i := strings.LastIndexByte(repo[slash+1:], ':'); i >= 0 {
		repo = repo[:slash+1+i]
	}
	return repo, digest
}

func normalizeRepo(repo string) string {
	first, _, hasSlash := strings.Cut(repo, "/")
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return repo
	}
	if hasSlash {
		return "docker.io/" + repo
	}
	return "docker.io/library/" + repo
}

func (e *Engine) resolve(ctx context.Context, ref, project string) (resolved, error) {
	owner, local, err := localProject(ref)
	if err != nil {
		return resolved{}, err
	}
	if local {
		if owner != project {
			return resolved{}, rejected(fmt.Errorf("image %s belongs to project %s, not %s; a project runs only its own builds", ref, owner, project))
		}
		img, inspectErr := e.inspectImage(ctx, ref)
		if inspectErr != nil {
			if isNotFound(inspectErr) {
				return resolved{}, fmt.Errorf("image %s is not on this server; build it again", ref)
			}
			return resolved{}, inspectErr
		}
		return resolved{ID: img.ID}, nil
	}
	repo, digest := splitRef(ref)
	if digest == "" {
		if digest, err = e.pull(ctx, ref); err != nil {
			return resolved{}, err
		}
	}
	img, inspectErr := e.inspectImage(ctx, ref)
	if inspectErr != nil && digest != "" && isNotFound(inspectErr) {
		if _, pullErr := e.pull(ctx, ref); pullErr != nil {
			return resolved{}, pullErr
		}
		img, inspectErr = e.inspectImage(ctx, ref)
	}
	if inspectErr != nil {
		return resolved{}, inspectErr
	}
	want := normalizeRepo(repo)
	for _, entry := range img.RepoDigests {
		got, d := splitRef(entry)
		if normalizeRepo(got) == want && (digest == "" || d == digest) {
			return resolved{ID: img.ID, RepoDigest: entry}, nil
		}
	}
	return resolved{}, fmt.Errorf("image %s has no repository digest for %s; push it to its registry again", ref, repo)
}

func (e *Engine) pull(ctx context.Context, ref string) (string, error) {
	ctx, cancel := pullCtx(ctx)
	defer cancel()
	res, err := e.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return "", fmt.Errorf("pull image %s: %w", ref, err)
	}
	digest := ""
	for msg, err := range res.JSONMessages(ctx) {
		if err != nil {
			return "", fmt.Errorf("pull image %s: %w", ref, err)
		}
		if msg.Error != nil {
			return "", fmt.Errorf("pull image %s: %s", ref, msg.Error.Message)
		}
		status := strings.ToLower(msg.Status)
		if i := strings.Index(status, "digest: "); i >= 0 {
			if fields := strings.Fields(msg.Status[i+len("digest: "):]); len(fields) > 0 && strings.HasPrefix(fields[0], "sha256:") {
				digest = fields[0]
			}
		}
	}
	return digest, nil
}

func (e *Engine) inspectImage(ctx context.Context, ref string) (client.ImageInspectResult, error) {
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ImageInspect(ctx, ref)
	if err != nil {
		return client.ImageInspectResult{}, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	return res, nil
}
