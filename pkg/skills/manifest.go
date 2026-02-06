package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Manifest tracks generated files for a platform.
type Manifest struct {
	Platform  string   `json:"platform"`
	Generated []string `json:"generated"`
	Timestamp string   `json:"timestamp"`
}

// ManifestFilename is the standard manifest filename written into each platform dir.
const ManifestFilename = ".loom-skills-manifest.json"

// WriteManifest writes a manifest file into the given directory.
func WriteManifest(dir, platform string, files []string) error {
	m := Manifest{
		Platform:  platform,
		Generated: files,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, ManifestFilename), append(data, '\n'), 0644)
}

// ReadManifest reads a manifest from the given directory, returning nil if not found.
func ReadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
