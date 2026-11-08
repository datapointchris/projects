package config

import (
	"fmt"
	"os"
)

// MIGRATION: projects once kept a database as a synced copy of an API, set under [sync] and by
// the variables below. Delete this file, Config.RetiredSync, its call in main.go and the warning
// in cli/data_source.go once no machine's config holds [sync] and no .envrc exports one of these.
var retiredSyncVariables = []string{
	"PROJECTS_SYNC",
	"PROJECTS_SYNC_URL",
	"PROJECTS_SYNC_TOKEN_COMMAND",
	"PROJECTS_SYNC_INTERVAL",
	"TODOUI_SYNC",
	"TODOUI_SYNC_URL",
	"TODOUI_SYNC_TOKEN_COMMAND",
	"TODOUI_SYNC_INTERVAL",
}

// RetiredSyncVariableRefusal names the first sync variable set to a value, or "" where none is.
// PROJECTS_SYNC_URL chose an API, and ignoring it would send the run wherever the config file
// points, which on a machine whose config names the production API is production.
func RetiredSyncVariableRefusal() string {
	for _, name := range retiredSyncVariables {
		if os.Getenv(name) != "" {
			return fmt.Sprintf("$%s is set, and projects keeps no synced copy of an API. Unset it. "+
				"api in the config file, written by `projects auth login <url>`, sends every command to an API.", name)
		}
	}
	return ""
}
