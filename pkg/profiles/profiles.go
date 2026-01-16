package profiles

import (
	kitprofiles "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/profiles"
)

type Profile = kitprofiles.Profile
type IncludeSpec = kitprofiles.IncludeSpec
type ProfileSet = kitprofiles.ProfileSet
type Manager = kitprofiles.Manager
type FilterResult = kitprofiles.FilterResult

func NewManager() *Manager {
	return kitprofiles.NewManager()
}

func DefaultProfilePath() string {
	return kitprofiles.DefaultProfilePath()
}
