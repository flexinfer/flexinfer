// Package detect provides project environment fingerprinting for devbox sandboxes.
package detect

// EnvFingerprint describes the detected environment for a project directory.
type EnvFingerprint struct {
	ProjectDir   string              `json:"project_dir"`
	ProjectName  string              `json:"project_name"`
	Languages    []LanguageSpec      `json:"languages"`
	SystemDeps   []string            `json:"system_deps,omitempty"`
	BuildTargets []string            `json:"build_targets,omitempty"`
	EnvVars      map[string]string   `json:"env_vars,omitempty"`
	Overrides    *ManifestOverride   `json:"overrides,omitempty"`
	DevContainer *DevContainerConfig `json:"devcontainer,omitempty"`
	Hash         string              `json:"hash"`
}

// LanguageSpec describes a detected language runtime and its dependency management.
type LanguageSpec struct {
	Language   string   `json:"language"`
	Version    string   `json:"version"`
	DepFile    string   `json:"dep_file,omitempty"`
	LockFile   string   `json:"lock_file,omitempty"`
	DepManager string   `json:"dep_manager,omitempty"`
	Tools      []string `json:"tools,omitempty"`
}

// ManifestOverride holds per-project overrides from .devbox.yaml.
type ManifestOverride struct {
	BaseImage  string            `yaml:"base_image,omitempty" json:"base_image,omitempty"`
	SystemDeps []string          `yaml:"system_deps,omitempty" json:"system_deps,omitempty"`
	Setup      []string          `yaml:"setup,omitempty" json:"setup,omitempty"`
	Env        map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Mounts     []MountSpec       `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	Limits     *LimitOverride    `yaml:"limits,omitempty" json:"limits,omitempty"`
	Network    *bool             `yaml:"network,omitempty" json:"network,omitempty"`
}

// MountSpec describes an additional bind mount for a sandbox.
type MountSpec struct {
	Host      string `yaml:"host" json:"host"`
	Container string `yaml:"container" json:"container"`
	ReadOnly  bool   `yaml:"read_only,omitempty" json:"read_only,omitempty"`
}

// LimitOverride allows per-project resource limit overrides.
type LimitOverride struct {
	MemoryMB int     `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`
	CPU      float64 `yaml:"cpu,omitempty" json:"cpu,omitempty"`
}
