package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/tui"
)

// tabProjects is the board's one tab, named for what it lists.
const tabProjects = "projects"

// The frame --once draws where COLUMNS or LINES is unset.
const (
	onceFallbackWidth  = 120
	onceFallbackHeight = 40
)

// tuiShown is `projects tui --json`: what the board holds as it opens.
// cli/schemas/board-snapshot.schema.json is its shape.
type tuiShown struct {
	SchemaVersion int                         `json:"schema_version"`
	View          string                      `json:"view"`
	Projects      []wire.ProjectWithItemCount `json:"projects"`
	Project       *wire.ProjectWithItemCount  `json:"project"`
	Items         []wire.ProjectItemInProject `json:"items"`
}

const tuiShownVersion = 1

func newTUICommand() *cobra.Command {
	var (
		view         string
		once, asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the board: projects on the left, the selected one's items on the right",
		Long: "Everything the commands do can be done here by key; ? lists the keys. The status\n" +
			"line names where the data lives: LOCAL and the database's path, API and its host,\n" +
			"or the sync state of a synced database. In API mode the board reads the API again\n" +
			"every [sync] interval, and R reads it now.\n" +
			"\n" +
			"--once prints one frame at COLUMNS by LINES, and --json what that frame is drawn\n" +
			"from, so an agent reads what a person reads. With no terminal and neither flag it\n" +
			"refuses.",
		Example: "  projects tui\n" +
			"  COLUMNS=160 LINES=50 projects tui --once > frame.txt\n" +
			"  projects tui --json | jq '.project.name'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if view != "" && view != tabProjects {
				return goclikit.UsageError(fmt.Errorf("--view takes %s, not %q", tabProjects, view))
			}
			if once && asJSON {
				return goclikit.UsageError(errors.New("--once prints a frame and --json what it is drawn from, so give one of them"))
			}
			printing := once || asJSON
			if !printing && (!term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))) {
				return goclikit.UsageError(errors.New("projects tui needs a terminal; --once prints one frame and --json what it is drawn from"))
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			where := tui.Where{DatabasePath: source.cfg.Local.DBPath, APIHost: source.apiHost()}
			if !printing {
				source.startSync()
			}
			app := tui.NewApp(source.boardBackend(), source.engine, where, source.cfg.Sync.Interval)
			if !printing {
				_, err := tea.NewProgram(app).Run()
				return err
			}
			if asJSON {
				if err := app.Read(); err != nil {
					return source.explain(err)
				}
				return encodeJSON(cmd.OutOrStdout(), boardShown(app))
			}
			width, height := envSize("COLUMNS", onceFallbackWidth), envSize("LINES", onceFallbackHeight)
			frame, err := app.Once(width, height)
			if err != nil {
				return source.explain(err)
			}
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), frame)
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "", "Open on this tab: "+tabProjects)
	cmd.Flags().BoolVar(&once, "once", false, "Print one frame at COLUMNS by LINES, then exit")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output what the board shows as it opens as JSON to stdout")
	return cmd
}

func boardShown(app *tui.App) tuiShown {
	projects, selected, items := app.Shown()
	shown := tuiShown{
		SchemaVersion: tuiShownVersion,
		View:          tabProjects,
		Projects:      wire.Map(projects, wire.NewProjectWithItemCount),
		Items:         wire.Map(items, wire.NewProjectItemInProject),
	}
	if selected != nil {
		project := wire.NewProjectWithItemCount(*selected)
		shown.Project = &project
	}
	return shown
}

// envSize reads COLUMNS or LINES, the size a caller with no terminal asks for.
func envSize(name string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return fallback
}
