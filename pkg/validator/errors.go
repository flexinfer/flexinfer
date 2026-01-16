package validator

import kitval "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/validator"

type ValidationSeverity = kitval.ValidationSeverity

const (
	SeverityError   = kitval.SeverityError
	SeverityWarning = kitval.SeverityWarning
)

type ValidationError = kitval.ValidationError
type ValidationResult = kitval.ValidationResult
