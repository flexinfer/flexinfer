package agentcontext

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CapabilityMap maps an agent id to its advertised capability tags.
type CapabilityMap map[string][]string

// agentCapabilitiesFile is the default path relative to the repo root.
// Override via the LOOM_AGENT_CAPABILITIES_FILE env var.
const agentCapabilitiesFile = "mcp/context/agent-capabilities.yaml"

// agentCapabilitiesSchema matches the YAML shape:
//
//	agents:
//	  - id: claude-code
//	    capabilities: [go, ts, python]
type agentCapabilitiesSchema struct {
	Agents []struct {
		ID           string   `yaml:"id"`
		Capabilities []string `yaml:"capabilities"`
	} `yaml:"agents"`
}

// LoadAgentCapabilities reads the capability YAML from the given path or the
// default location. Missing file returns an empty map and a nil error so the
// dispatcher degrades gracefully in environments where the seed file has not
// been provisioned yet.
func LoadAgentCapabilities(path string) (CapabilityMap, error) {
	if path == "" {
		if v := os.Getenv("LOOM_AGENT_CAPABILITIES_FILE"); v != "" {
			path = v
		} else {
			path = agentCapabilitiesFile
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CapabilityMap{}, nil
		}
		return nil, fmt.Errorf("read capabilities: %w", err)
	}

	var doc agentCapabilitiesSchema
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse capabilities: %w", err)
	}

	out := make(CapabilityMap, len(doc.Agents))
	for _, a := range doc.Agents {
		if a.ID == "" {
			continue
		}
		out[a.ID] = append([]string(nil), a.Capabilities...)
	}
	return out, nil
}
