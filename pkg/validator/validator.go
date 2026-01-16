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
