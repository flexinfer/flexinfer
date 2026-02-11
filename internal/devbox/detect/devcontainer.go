package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DevContainerConfig represents a subset of the .devcontainer/devcontainer.json spec.
type DevContainerConfig struct {
	Image             string             `json:"image,omitempty"`
	Dockerfile        string             `json:"dockerFile,omitempty"`
	Build             *DevContainerBuild `json:"build,omitempty"`
	Features          map[string]any     `json:"features,omitempty"`
	PostCreateCommand any                `json:"postCreateCommand,omitempty"`
	ContainerEnv      map[string]string  `json:"containerEnv,omitempty"`
	RunArgs           []string           `json:"runArgs,omitempty"`
	ForwardPorts      []int              `json:"forwardPorts,omitempty"`
	RemoteUser        string             `json:"remoteUser,omitempty"`
}

// DevContainerBuild holds build-related fields from devcontainer.json.
type DevContainerBuild struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// loadDevContainer reads and parses .devcontainer/devcontainer.json.
// Returns nil, nil if the file does not exist.
func loadDevContainer(projectDir string) (*DevContainerConfig, error) {
	candidates := []string{
		filepath.Join(projectDir, ".devcontainer", "devcontainer.json"),
		filepath.Join(projectDir, ".devcontainer.json"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		var cfg DevContainerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		return &cfg, nil
	}

	return nil, nil
}

// resolveDockerfile returns the Dockerfile path from a DevContainerConfig.
// Checks both top-level `dockerFile` and `build.dockerfile`.
func (dc *DevContainerConfig) ResolveDockerfile() string {
	if dc.Dockerfile != "" {
		return dc.Dockerfile
	}
	if dc.Build != nil && dc.Build.Dockerfile != "" {
		return dc.Build.Dockerfile
	}
	return ""
}

// resolvePostCreateCommand normalizes postCreateCommand which can be a string or array.
func (dc *DevContainerConfig) ResolvePostCreateCommand() string {
	if dc.PostCreateCommand == nil {
		return ""
	}
	switch v := dc.PostCreateCommand.(type) {
	case string:
		return v
	case []any:
		// Join array elements with &&
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			result := parts[0]
			for _, p := range parts[1:] {
				result += " && " + p
			}
			return result
		}
	}
	return ""
}
