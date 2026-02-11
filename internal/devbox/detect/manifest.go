package detect

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const manifestFileName = ".devbox.yaml"

// loadManifest reads and parses a .devbox.yaml file from the project directory.
// Returns nil, nil if the file does not exist.
func loadManifest(projectDir string) (*ManifestOverride, error) {
	path := filepath.Join(projectDir, manifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var m ManifestOverride
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
