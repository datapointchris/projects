package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermboard/printedframe"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/tui"
)

// The frame --once draws where COLUMNS and LINES are unset and stdout is no terminal.
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

// viewDependencies is the view --json answers nothing for, because a command already prints the map.
const viewDependencies = "dependencies"

func newTUICommand() *cobra.Command {
	var (
		view         string
		once, asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the board: projects and their items, every item, or the dependency map",
		Long: "Everything the commands do can be done here by key; ? lists the keys. Tab cycles\n" +
			"the three views, and --view opens on one. The status line names where the data\n" +
			"lives: LOCAL and the database's path, or API and its host. In API mode the board\n" +
			"reads the API again on a timer, and R reads it now.\n" +
			"\n" +
			"--once prints one frame at COLUMNS by LINES, else at the terminal's size, else at\n" +
			"120 by 40, and --json what that frame is drawn from, so an agent reads what a\n" +
			"person reads. The dependency map's --json is `projects items dependencies tree\n" +
			"--json`. With no terminal and neither flag it refuses.",
		Example: "  projects tui\n" +
			"  projects tui --view items\n" +
			"  COLUMNS=160 LINES=50 projects tui --once --view dependencies > frame.txt\n" +
			"  projects tui --json | jq '.project.name'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if view != "" && !slices.Contains(tui.ViewNames(), view) {
				return goclikit.UsageError(fmt.Errorf("--view takes %s, not %q", strings.Join(tui.ViewNames(), ", "), view))
			}
			if asJSON && view == viewDependencies {
				return goclikit.UsageError(errors.New("the dependency map's --json is projects items dependencies tree --json"))
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
			app := tui.NewApp(source.boardBackend(), where)
			if view != "" {
				app.OpenOn(view)
			}
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
			width, height := printedframe.Size(onceFallbackWidth, onceFallbackHeight)
			frame, err := app.Once(width, height)
			if err != nil {
				return source.explain(err)
			}
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), frame)
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "", "Open on this view: "+strings.Join(tui.ViewNames(), ", "))
	cmd.Flags().BoolVar(&once, "once", false, "Print one frame at COLUMNS by LINES, else the terminal's size, then exit")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output what the board shows as it opens as JSON to stdout")
	return cmd
}

func boardShown(app *tui.App) tuiShown {
	projects, selected, items := app.Shown()
	shown := tuiShown{
		SchemaVersion: tuiShownVersion,
		View:          app.ViewName(),
		Projects:      wire.Map(projects, wire.NewProjectWithItemCount),
		Items:         wire.Map(items, wire.NewProjectItemInProject),
	}
	if selected != nil {
		project := wire.NewProjectWithItemCount(*selected)
		shown.Project = &project
	}
	return shown
}
