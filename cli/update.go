package cli

import (
	"runtime/debug"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate"
	"github.com/spf13/cobra"
)

// version is set at build time via ldflags. Falls back to Go module info.
var version = ""

func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// UpdateConfig describes where todoui's releases come from.
//
// Exported because main wires the same config into both halves: the `update`
// command that installs a release, and the daily check that mentions one. Two
// copies would drift, and a drifted notify config points a user at a release
// the update command will not install.
func UpdateConfig() goselfupdate.Config {
	return goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "todoui",
		Binary:  "todoui",
		Version: Version(),
	}
}

func UpdateCmd() *cobra.Command {
	return goclikit.UpdateCommand(UpdateConfig())
}

func VersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the current version",
		Example: "  todoui version    print the version of this binary, such as v1.4.0",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(Version())
		},
	}
}
