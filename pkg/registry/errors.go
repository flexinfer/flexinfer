package registry

import "errors"

var (
	// ErrRegistryNotConfigured is returned when no registry URL is available.
	ErrRegistryNotConfigured = errors.New("registry URL not configured")

	// ErrUnknownRegistryType is returned for unrecognized registry types.
	ErrUnknownRegistryType = errors.New("unknown registry type")

	// ErrPullNotSupported is returned when a registry implementation
	// does not support pulling artifacts.
	ErrPullNotSupported = errors.New("pull not supported by this registry")
)
