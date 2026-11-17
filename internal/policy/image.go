package policy

import (
	"fmt"
	"strings"
)

type imageRef struct {
	repo   string // normalised repository, e.g. docker.io/prom/node-exporter
	digest string // sha256:<64 hex>, empty if the image has no digest
	raw    string
}

func parseBlockImage(s string) (imageRef, error) {
	repo, digest, tag, err := splitImage(s)
	if err != nil || tag != "" || digest == "" || !validDigest(digest) {
		return imageRef{}, fmt.Errorf("image must be name@sha256:<digest>")
	}
	return imageRef{repo: normalizeRepo(repo), digest: digest, raw: s}, nil
}

func parseSpecImage(s string) imageRef {
	repo, digest, _, err := splitImage(s)
	if err != nil {
		return imageRef{raw: s}
	}
	out := imageRef{raw: s}
	if repo != "" {
		out.repo = normalizeRepo(repo)
	}
	if validDigest(digest) {
		out.digest = digest
	}
	return out
}

func splitImage(s string) (repo, digest, tag string, err error) {
	if s == "" {
		return "", "", "", fmt.Errorf("empty image")
	}
	name := s
	if i := strings.LastIndex(s, "@"); i >= 0 {
		name, digest = s[:i], s[i+1:]
	}
	// A tag is a colon in the last path element, not a registry port.
	i := strings.LastIndex(name, "/")
	last := name
	if i >= 0 {
		last = name[i+1:]
	}
	if j := strings.LastIndex(last, ":"); j >= 0 {
		tag = last[j+1:]
		name = name[:len(name)-len(tag)-1]
	}
	return name, digest, tag, nil
}

func validDigest(d string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(d, prefix) {
		return false
	}
	hex := d[len(prefix):]
	if len(hex) != 64 {
		return false
	}
	for _, c := range hex {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return false
	}
	return true
}

func normalizeRepo(name string) string {
	name = strings.TrimPrefix(name, "docker.io/")
	slash := strings.Index(name, "/")
	if slash < 0 {
		return "docker.io/library/" + name
	}
	first := name[:slash]
	// A first path element with a "." or ":" is a registry.
	if strings.ContainsAny(first, ".:") {
		return name
	}
	return "docker.io/" + name
}

func imagesMatch(block, spec imageRef) bool {
	if block.digest == "" || spec.digest == "" {
		return false
	}
	return block.repo == spec.repo && block.digest == spec.digest
}

func digestForm(image string) string {
	if image == "" {
		return "name@sha256:<digest>"
	}
	repo, digest, _, err := splitImage(image)
	if err != nil || repo == "" {
		return "name@sha256:<digest>"
	}
	if validDigest(digest) {
		return repo + "@" + digest
	}
	return repo + "@sha256:<digest>"
}
