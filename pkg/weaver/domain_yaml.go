package weaver

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// domainsFile is the YAML format for domain definitions.
type domainsFile struct {
	Domains []SubAgent `yaml:"domains"`
}

// DefaultDomainsPath returns the default path for user domain definitions.
func DefaultDomainsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "weaver-domains.yaml")
}

// LoadDomainsFromFile reads domain definitions from a YAML file.
// Returns nil, nil if the file doesn't exist.
func LoadDomainsFromFile(path string) ([]SubAgent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read domains file: %w", err)
	}

	var df domainsFile
	if err := yaml.Unmarshal(data, &df); err != nil {
		return nil, fmt.Errorf("parse domains YAML: %w", err)
	}

	for i, d := range df.Domains {
		if d.Name == "" {
			return nil, fmt.Errorf("domain at index %d missing name", i)
		}
		if len(d.Tools) == 0 {
			return nil, fmt.Errorf("domain %q has no tools", d.Name)
		}
	}

	return df.Domains, nil
}

// MergeDomainsIntoRegistry loads YAML domains and merges them into the
// registry. YAML domains override built-in defaults with the same name.
func MergeDomainsIntoRegistry(reg *DomainRegistry, path string) error {
	agents, err := LoadDomainsFromFile(path)
	if err != nil {
		return err
	}
	for _, a := range agents {
		reg.Register(a)
	}
	return nil
}
