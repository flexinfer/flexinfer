// Package registry provides a pluggable model registry adapter layer.
// Each adapter implements the ModelRegistry interface to provide model discovery,
// resolution, and pulling from different artifact sources.
package registry

import (
	"context"
	"fmt"
	"time"
)

// ModelRegistry defines the interface for model artifact sources.
// Implementations provide access to OCI registries, HuggingFace Hub,
// Ollama library, or other model distribution systems.
type ModelRegistry interface {
	// List returns models matching the filter criteria.
	List(ctx context.Context, filter ListFilter) ([]ModelEntry, error)

	// Pull downloads model artifacts to the destination path.
	Pull(ctx context.Context, ref string, destPath string, opts PullOptions) error

	// Resolve retrieves metadata for a model reference without downloading.
	Resolve(ctx context.Context, ref string) (*ModelMetadata, error)

	// Type returns the registry type identifier (e.g., "oci", "huggingface", "ollama").
	Type() string
}

// ListFilter constrains which models are returned from List.
type ListFilter struct {
	// Query is a search string to match against model names and tags.
	Query string

	// Tags filters models that have all specified tags.
	Tags []string

	// Limit caps the number of results.
	Limit int
}

// PullOptions configures the pull behavior.
type PullOptions struct {
	// SecretName is the Kubernetes secret containing credentials.
	SecretName string

	// Namespace is the Kubernetes namespace for secret lookup.
	Namespace string

	// Force re-downloads even if the artifact exists locally.
	Force bool
}

// ModelEntry represents a model in a registry listing.
type ModelEntry struct {
	// Name is the model name (e.g., "meta-llama/Llama-2-7b-chat-hf").
	Name string `json:"name"`

	// Registry is the source registry type.
	Registry string `json:"registry"`

	// Reference is the full qualified reference for pulling.
	Reference string `json:"reference"`

	// Tags are metadata tags for categorization.
	Tags []string `json:"tags,omitempty"`

	// Size is the approximate model size in bytes.
	Size int64 `json:"size,omitempty"`

	// UpdatedAt is when the model was last updated.
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// ModelMetadata contains detailed information about a model artifact.
type ModelMetadata struct {
	// Name is the model name.
	Name string `json:"name"`

	// Digest is the content-addressable digest (e.g., sha256:...).
	Digest string `json:"digest,omitempty"`

	// Size is the total artifact size in bytes.
	Size int64 `json:"size"`

	// Format describes the model format (e.g., "safetensors", "gguf", "oci").
	Format string `json:"format,omitempty"`

	// Tags are metadata tags.
	Tags []string `json:"tags,omitempty"`

	// CreatedAt is when the artifact was created.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// registryMap holds registered adapter factories.
var registries = make(map[string]func() ModelRegistry)

// Register adds a registry adapter factory.
func Register(name string, factory func() ModelRegistry) {
	registries[name] = factory
}

// Get returns a registry adapter by type name.
func Get(name string) (ModelRegistry, error) {
	factory, ok := registries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRegistryType, name)
	}
	return factory(), nil
}

// Types returns all registered registry type names.
func Types() []string {
	types := make([]string, 0, len(registries))
	for k := range registries {
		types = append(types, k)
	}
	return types
}
