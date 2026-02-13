package validator

import kitval "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/validator"

type Validator = kitval.Validator

func New(repoRoot, homeDir string) *Validator {
	return kitval.New(repoRoot, homeDir)
}

func HasErrors(results []*kitval.ValidationResult) bool {
	return kitval.HasErrors(results)
}

func SummaryString(results []*kitval.ValidationResult) string {
	return kitval.SummaryString(results)
}

// Upstream schema validation wrappers.

func ValidateClaudeSettings(filePath string, content []byte) *kitval.ValidationResult {
	return kitval.ValidateClaudeSettings(filePath, content)
}

func ValidateGeminiSettings(filePath string, content []byte) *kitval.ValidationResult {
	return kitval.ValidateGeminiSettings(filePath, content)
}

func ValidateCodexConfig(filePath string, content []byte) *kitval.ValidationResult {
	return kitval.ValidateCodexConfig(filePath, content)
}

type UpstreamSchemaInfo = kitval.UpstreamSchemaInfo

func UpstreamSchemas() []UpstreamSchemaInfo {
	return kitval.UpstreamSchemas()
}

func GetEmbeddedSchema(name string) ([]byte, bool) {
	return kitval.GetEmbeddedSchema(name)
}
