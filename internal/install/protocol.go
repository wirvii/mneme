package install

// operatingManual returns the mneme operating manual markdown that is injected
// into ~/.claude/CLAUDE.md via upsertManagedBlock. The manual teaches an agent
// how to use mneme autonomously (memory lifecycle, SDD, roles, delegation) and
// is the single source of process instructions — lean enough to fit in every
// session context without pagination.
//
// The content is embedded from assets/operating-manual.md at build time.
// Edit that file to change the manual; this function is only the accessor.
func operatingManual() string {
	return string(operatingManualAsset)
}

// OperatingManual is the exported version of operatingManual for use by callers
// outside this package (e.g. the CLI init command) that need to wire the real
// operating manual content into service.InitService without importing install
// as a hard dependency in the service layer.
func OperatingManual() string {
	return operatingManual()
}

// UpsertManagedBlock is the exported version of upsertManagedBlock for use by
// callers outside this package (e.g. the CLI init command) that need to wire
// the real managed-block primitive into service.InitService.
func UpsertManagedBlock(filePath, content string) error {
	return upsertManagedBlock(filePath, content)
}
