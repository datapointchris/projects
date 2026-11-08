// Package cli is the projects command tree. Every command reaches its data through
// the API's client: over the network when the config names an API, and through the
// API's own routes served inside this process when it does not.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/datapointchris/gotermstyle/cobrahelp"
	"github.com/spf13/cobra"
)

// noInput is bound to --no-input, which forces the non-interactive path from a
// terminal. pflag resets it on each NewRootCommand, so a test never inherits it.
var noInput bool

// apiOverride is bound to --api, the one value that outranks the config file.
var apiOverride string

// withAPIFlag declares --api on cmd and everything under it. Only the commands
// that reach data or log in carry it.
func withAPIFlag(cmd *cobra.Command) *cobra.Command {
	cmd.PersistentFlags().StringVar(&apiOverride, "api", "",
		"Use this API instead of the config file's; it must trust the file's login server")
	return cmd
}

// The root's help sections.
const (
	groupReading  = "reading"
	groupChanging = "changing"
	groupItems    = "items"
	groupScreen   = "screen"
	groupSetup    = "setup"
)

// splitReadingFromChanging gives a namespace the two sections its verbs go under,
// so the half that writes is visible without reading each Short.
func splitReadingFromChanging(cmd *cobra.Command) {
	cmd.AddGroup(
		&cobra.Group{ID: groupReading, Title: "Reading commands:"},
		&cobra.Group{ID: groupChanging, Title: "Changing commands:"},
	)
}

func inGroup(id string, cmds ...*cobra.Command) []*cobra.Command {
	for _, cmd := range cmds {
		cmd.GroupID = id
	}
	return cmds
}

// exitCode chooses the process's exit code with nothing printed, for a status
// command reporting a valid state that is not success.
type exitCode int

func (e exitCode) Error() string { return "" }

// statusNoAnswer is the one failure where the same command run later can succeed:
// the API was not reached, timed out, or answered 429 or 5xx.
const statusNoAnswer = 4

// projectHints is the command that finds a valid project name. A name resolves
// whatever the project's status, and the API names the projects near a miss.
var projectHints = []string{
	"A project is named in full or by its id; every project: projects list --status all",
}

func NewRootCommand() *cobra.Command {
	root := goclikit.AsNamespace(&cobra.Command{
		Use:   "projects",
		Short: "Projects, the items that finish them, and the order to take them in",
		Long: "A project is bounded work, and its items are the steps that finish it. Each item\n" +
			"has tasks, dependencies on other items, and a place in one or more projects.\n" +
			"\n" +
			"The project verbs sit at the root, and the item verbs under `projects items`:\n" +
			"`projects list` becomes `projects items list` by adding the noun. Every read\n" +
			"takes --json. `projects tui` opens the board, where everything here can also be\n" +
			"done by key. Run any partial command bare to see what comes next.\n" +
			"\n" +
			"Where the data lives is the config file's choice. With an api key there, every\n" +
			"command reads and writes that API, after `projects auth login <url>`. With none,\n" +
			"they read and write a database on this machine, and need no server.",
		Example: "  projects next --limit 1\n" +
			"  projects show \"Kitchen remodel\"\n" +
			"  projects items create --project \"Kitchen remodel\" --title \"Get three quotes\"\n" +
			"  projects items show 42\n" +
			"  projects tui",
		Version:       Version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// An unknown word reaches the namespace's own refusal, which exits 2, rather
		// than cobra's root validator, which exits 1.
		Args: cobra.ArbitraryArgs,
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return goclikit.UsageError(err)
	})
	cobrahelp.Install(root)
	goclikit.WithRecoveryHints(root, projectHints...)

	root.PersistentFlags().BoolVar(&noInput, "no-input", false,
		"Never prompt; fail naming the flag that would have answered")

	// -v stays free for a verbose flag, so --version keeps no shorthand.
	root.InitDefaultVersionFlag()
	if f := root.Flags().Lookup("version"); f != nil {
		f.Shorthand = ""
	}

	root.AddGroup(
		&cobra.Group{ID: groupReading, Title: "Reading projects:"},
		&cobra.Group{ID: groupChanging, Title: "Changing projects:"},
		&cobra.Group{ID: groupItems, Title: "Items:"},
		&cobra.Group{ID: groupScreen, Title: "The board:"},
		&cobra.Group{ID: groupSetup, Title: "Setup:"},
	)
	root.SetCompletionCommandGroupID(groupSetup)
	root.AddCommand(inGroup(groupReading,
		withAPIFlag(newProjectsListCommand()),
		withAPIFlag(newProjectsNextCommand()),
		withAPIFlag(newProjectsShowCommand()),
	)...)
	root.AddCommand(inGroup(groupChanging,
		withAPIFlag(newProjectsCreateCommand()),
		withAPIFlag(newProjectsEditCommand()),
		withAPIFlag(newProjectsShelveCommand()),
		withAPIFlag(newProjectsCompleteCommand()),
		withAPIFlag(newProjectsCloseCommand()),
		withAPIFlag(newProjectsReopenCommand()),
		withAPIFlag(newProjectsDeleteCommand()),
	)...)
	root.AddCommand(inGroup(groupItems, withAPIFlag(newItemsCommand()))...)
	root.AddCommand(inGroup(groupScreen, withAPIFlag(newTUICommand()))...)
	root.AddCommand(inGroup(groupSetup, newAuthCommand(), newSyncCommand(), VersionCmd(), UpdateCmd())...)
	return root
}

// Execute runs the command tree, prints any error to stderr, and returns the
// process exit code.
func Execute() int {
	err := goclikit.Execute(context.Background(), NewRootCommand(), autoupdate.Config{Update: UpdateConfig()},
		goclikit.WithNotFound(notFound))
	var code exitCode
	if err != nil && !errors.As(err, &code) && !errors.Is(err, goclikit.ErrReported) {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
	}
	return exitCodeFor(err)
}

// exitCodeFor is 0 for success, 2 for a usage mistake, 4 where no answer came,
// and 1 for anything the API or the command refused.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if errors.Is(err, goclikit.ErrUsage) {
		return 2
	}
	var silent noAnswerError
	if errors.As(err, &silent) {
		return statusNoAnswer
	}
	return 1
}
