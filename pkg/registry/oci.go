package registry

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// OCIRegistry implements ModelRegistry for OCI-compliant registries (Harbor, Docker Hub, etc.).
// Uses oras CLI or Go SDK for artifact operations.
type OCIRegistry struct {
	// RegistryURL is the base URL of the OCI registry.
	RegistryURL string
	// Username for registry authentication.
	Username string
	// Password for registry authentication.
	Password string
}

func init() {
	Register("oci", func() ModelRegistry { return &OCIRegistry{} })
}

func (r *OCIRegistry) Type() string { return "oci" }

func (r *OCIRegistry) List(ctx context.Context, filter ListFilter) ([]ModelEntry, error) {
	// OCI registries don't natively support search without catalog API.
	// Harbor provides a REST API for this; generic registries rely on the catalog endpoint.
	if r.RegistryURL == "" {
		return nil, ErrRegistryNotConfigured
	}

	// Use oras to discover tags for a repo when query looks like a repo ref.
	if filter.Query != "" && strings.Contains(filter.Query, "/") {
		ref := r.RegistryURL + "/" + filter.Query
		out, err := r.runOras(ctx, "repo", "tags", ref)
		if err != nil {
			return nil, fmt.Errorf("oras repo tags: %w", err)
		}
		var entries []ModelEntry
		for _, tag := range strings.Split(strings.TrimSpace(out), "\n") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			entries = append(entries, ModelEntry{
				Name:      filter.Query + ":" + tag,
				Registry:  "oci",
				Reference: ref + ":" + tag,
			})
		}
		return entries, nil
	}

	return nil, nil
}

func (r *OCIRegistry) Pull(ctx context.Context, ref string, destPath string, opts PullOptions) error {
	args := []string{"pull", ref, "-o", destPath}
	if r.Username != "" {
		args = append(args, "--username", r.Username, "--password", r.Password)
	}
	_, err := r.runOras(ctx, args...)
	return err
}

func (r *OCIRegistry) Resolve(ctx context.Context, ref string) (*ModelMetadata, error) {
	out, err := r.runOras(ctx, "manifest", "fetch", ref)
	if err != nil {
		return nil, fmt.Errorf("oras manifest fetch: %w", err)
	}
	return &ModelMetadata{
		Name:   ref,
		Format: "oci",
		Digest: extractDigestFromManifest(out),
	}, nil
}

func (r *OCIRegistry) runOras(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "oras", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("oras %s: %w (output: %s)", strings.Join(args[:1], " "), err, string(out))
	}
	return string(out), nil
}

// extractDigestFromManifest parses a simple digest from oras manifest output.
func extractDigestFromManifest(manifest string) string {
	for _, line := range strings.Split(manifest, "\n") {
		if strings.Contains(line, "digest") && strings.Contains(line, "sha256:") {
			start := strings.Index(line, "sha256:")
			if start >= 0 {
				end := start + 71 // sha256: + 64 hex chars
				if end > len(line) {
					end = len(line)
				}
				return strings.TrimRight(line[start:end], `"}, `)
			}
		}
	}
	return ""
}
