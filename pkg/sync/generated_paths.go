package sync

// primaryHomeGeneratedFile returns the primary generated filename expected in
// the home profile directory.
func primaryHomeGeneratedFile(p *Profile) string {
	if p != nil && p.HomeGeneratedFile != "" {
		return p.HomeGeneratedFile
	}
	if p == nil {
		return ""
	}
	return p.GeneratedFile
}

// mapRepoGeneratedToHome maps a generated file path (relative to repo profile
// dir) to its destination relative path in the home profile dir.
func mapRepoGeneratedToHome(p *Profile, repoRel string) string {
	if p != nil && p.HomeGeneratedFile != "" && repoRel == p.GeneratedFile {
		return p.HomeGeneratedFile
	}
	return repoRel
}
