package validator

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/mcp_json.json
var mcpJSONSchemaBytes []byte

var mcpJSONSchema *jsonschema.Schema

func init() {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("mcp_json.json", strings.NewReader(string(mcpJSONSchemaBytes))); err != nil {
		panic(fmt.Sprintf("failed to load embedded JSON schema: %v", err))
	}
	var err error
	mcpJSONSchema, err = compiler.Compile("mcp_json.json")
	if err != nil {
		panic(fmt.Sprintf("failed to compile JSON schema: %v", err))
	}
}

// ValidateJSONSchema validates JSON config content against the MCP schema.
func ValidateJSONSchema(target, filePath string, content []byte) *ValidationResult {
	result := &ValidationResult{
		Target: target,
		File:   filePath,
		Valid:  true,
	}

	// Parse JSON
	var data interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		result.AddError(CodeInvalidSchema, "", fmt.Sprintf("invalid JSON: %v", err))
		result.Valid = false
		return result
	}

	// Validate against schema
	if err := mcpJSONSchema.Validate(data); err != nil {
		// Parse validation errors
		if ve, ok := err.(*jsonschema.ValidationError); ok {
			for _, cause := range flattenValidationErrors(ve) {
				field := cause.InstanceLocation
				if field == "" {
					field = "/"
				}
				result.AddError(CodeInvalidSchema, field, cause.Message)
			}
		} else {
			result.AddError(CodeInvalidSchema, "", err.Error())
		}
		result.Valid = false
	}

	// Additional semantic validation
	validateJSONSemantics(data, result)

	result.Valid = !result.HasErrors()
	return result
}

// flattenValidationErrors extracts all leaf errors from a validation error tree.
func flattenValidationErrors(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	var errors []*jsonschema.ValidationError
	if len(ve.Causes) == 0 {
		errors = append(errors, ve)
	}
	for _, cause := range ve.Causes {
		errors = append(errors, flattenValidationErrors(cause)...)
	}
	return errors
}

// validateJSONSemantics performs additional semantic checks beyond schema validation.
func validateJSONSemantics(data interface{}, result *ValidationResult) {
	m, ok := data.(map[string]interface{})
	if !ok {
		return
	}

	servers, ok := m["mcpServers"].(map[string]interface{})
	if !ok {
		result.AddError(CodeMissingRootKey, "", "missing or invalid mcpServers key")
		return
	}

	for name, serverData := range servers {
		server, ok := serverData.(map[string]interface{})
		if !ok {
			continue
		}

		field := fmt.Sprintf("mcpServers.%s", name)

		// Check command is not empty
		cmd, _ := server["command"].(string)
		if cmd == "" {
			result.AddError(CodeMissingCommand, field+".command", "command is required")
		}

		// Check args type if present
		if args, exists := server["args"]; exists {
			if _, ok := args.([]interface{}); !ok {
				result.AddError(CodeInvalidArgsType, field+".args", "args must be an array")
			}
		}

		// Check env type if present
		if env, exists := server["env"]; exists {
			if _, ok := env.(map[string]interface{}); !ok {
				result.AddError(CodeInvalidEnvType, field+".env", "env must be an object")
			}
		}
	}
}

// TOMLServerConfig represents a server configuration in TOML format.
type TOMLServerConfig struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Description string            `toml:"description"`
	Hint        string            `toml:"hint"`
	Timeout     int               `toml:"timeout"`
	AlwaysAllow []string          `toml:"always_allow"`
	Env         map[string]string `toml:"env"`
}

// TOMLConfig represents the TOML config file structure.
type TOMLConfig struct {
	MCPServers map[string]TOMLServerConfig `toml:"mcp_servers"`
}

// ValidateTOMLStructure validates TOML config structure for Codex/Kilocode/Gemini.
func ValidateTOMLStructure(target, filePath string, content []byte) *ValidationResult {
	result := &ValidationResult{
		Target: target,
		File:   filePath,
		Valid:  true,
	}

	var cfg TOMLConfig
	if err := toml.Unmarshal(content, &cfg); err != nil {
		result.AddError(CodeInvalidSchema, "", fmt.Sprintf("invalid TOML: %v", err))
		result.Valid = false
		return result
	}

	// Check for mcp_servers section
	if len(cfg.MCPServers) == 0 {
		result.AddError(CodeMissingRootKey, "", "missing or empty mcp_servers section")
		result.Valid = false
		return result
	}

	// Validate each server
	for name, server := range cfg.MCPServers {
		field := fmt.Sprintf("mcp_servers.%s", name)

		// Command is required
		if server.Command == "" {
			result.AddError(CodeMissingCommand, field+".command", "command is required")
		}

		// Timeout must be non-negative
		if server.Timeout < 0 {
			result.AddError(CodeInvalidTimeout, field+".timeout",
				fmt.Sprintf("timeout must be non-negative, got %d", server.Timeout))
		}
	}

	result.Valid = !result.HasErrors()
	return result
}

// IsJSONTarget returns true if the target uses JSON format.
func IsJSONTarget(target string) bool {
	switch target {
	case "claude", "claude_desktop", "vscode", "antigravity":
		return true
	default:
		return false
	}
}

// IsTOMLTarget returns true if the target uses TOML format.
func IsTOMLTarget(target string) bool {
	switch target {
	case "codex", "kilocode", "gemini":
		return true
	default:
		return false
	}
}
