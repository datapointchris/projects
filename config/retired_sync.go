package config

// MIGRATION: projects once kept a database as a synced copy of an API, set under [sync] and by
// the variables below. Delete this file, Config.RetiredSync, warnOfRetiredSyncSection in
// cli/data_source.go, dropSyncTables in store/sqlite/open.go, and the passages on them in README
// and CLAUDE.md once no machine's config holds [sync], no .envrc exports one of these, and no
// machine's database still holds pending_sync.

// PROJECTS_SYNC_URL chose an API, and ignoring it would send the run wherever the config file
// points, which on a machine whose config names the production API is production.
func init() {
	for _, name := range []string{
		"PROJECTS_SYNC",
		"PROJECTS_SYNC_URL",
		"PROJECTS_SYNC_TOKEN_COMMAND",
		"PROJECTS_SYNC_INTERVAL",
		"TODOUI_SYNC",
		"TODOUI_SYNC_URL",
		"TODOUI_SYNC_TOKEN_COMMAND",
		"TODOUI_SYNC_INTERVAL",
	} {
		retiredVariables = append(retiredVariables, retiredVariable{
			name: name,
			remedy: "and projects keeps no synced copy of an API. Unset it. api in the config file, " +
				"written by `projects auth login <url>`, sends every command to an API.",
		})
	}
}
