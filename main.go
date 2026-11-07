package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/spf13/cobra"

	"github.com/datapointchris/todoui/backend"
	"github.com/datapointchris/todoui/cli"
	"github.com/datapointchris/todoui/config"
	"github.com/datapointchris/todoui/db"
	"github.com/datapointchris/todoui/repos"
	"github.com/datapointchris/todoui/sync"
	"github.com/datapointchris/todoui/tui"
)

func main() {
	autoConfig := autoupdate.Config{Update: cli.UpdateConfig()}
	if err := goclikit.Execute(context.Background(), rootCmd(), autoConfig); err != nil {
		if !errors.Is(err, goclikit.ErrReported) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		// 2 says the command line was wrong rather than the run, which is the
		// only failure a caller should retry with different arguments.
		if errors.Is(err, goclikit.ErrUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// localOnlyBackend is the backend with sync off, where this database is the
// authority that numbers items.
func localOnlyBackend(d *sql.DB, reposRegistryPath string) *backend.LocalBackend {
	return backend.NewLocalBackend(d, backend.AssigningItemNumbers(), backend.RefusingRepoNames(reposRegistryPath))
}

// needsBackend reports whether a command reads or writes todoui's own data.
//
// PersistentPreRunE opens the database and runs migrations, and cobra runs it
// for the shell-completion callback like any other command — so every TAB press
// opened the database, and on a new machine created it. `version` is what a
// script calls to find out what is installed, `help` answers from the command
// tree, and `update` replaces the binary. None of them has anything to read.
//
// The ancestry is walked rather than only the leaf, because `completion` owns a
// subcommand per shell: `todoui completion zsh` has leaf name "zsh".
//
// `--help` never reaches here at all — cobra answers the flag before running
// this — which is why the list names the command and not the flag.
func needsBackend(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		switch current.Name() {
		case "help", "version", "update", "completion",
			cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return false
		}
	}
	return true
}

func rootCmd() *cobra.Command {
	var b backend.Backend
	var database *sql.DB
	var syncEngine *sync.Engine
	var cfg *config.Config

	root := &cobra.Command{
		Use:   "todoui",
		Short: "Personal project organization",
		Long: "Personal project organization.\n\n" +
			"Run `todoui` to open the TUI, where everything happens. With sync enabled it\n" +
			"pushes each edit as you make it and pulls in the background on every interval,\n" +
			"so it never needs a sync by hand.\n\n" +
			"Reach for `todoui sync` when you want the API's state now rather than at the\n" +
			"next interval, or need --adopt or --force: it pushes anything queued, then pulls.",
		Example: "  todoui         open the TUI, syncing in the background when sync is enabled\n" +
			"  todoui sync    push queued edits and pull now, without waiting for the interval",
		Version: cli.Version(),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !needsBackend(cmd) {
				return nil
			}
			var err error
			cfg, err = config.Load()
			if err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(cfg.Local.DBPath), 0o755); err != nil {
				return fmt.Errorf("creating data directory: %w", err)
			}
			d, err := db.Open(cfg.Local.DBPath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			database = d

			if cfg.Sync.Enabled {
				local := backend.NewLocalBackend(d, backend.RefusingRepoNames(repos.DefaultPath()))
				syncEngine = sync.New(d, cfg.Sync.APIURL, cfg.Sync.TokenCommand)
				syncEngine.Start()
				b = sync.NewSyncBackend(local, syncEngine)
			} else {
				b = localOnlyBackend(d, repos.DefaultPath())
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			app := tui.NewApp(b, syncEngine, cfg.Local.DBPath, cfg.Sync.Interval)
			_, err := tea.NewProgram(app).Run()
			return err
		},
		PersistentPostRunE: func(_ *cobra.Command, _ []string) error {
			if syncEngine != nil {
				syncEngine.Stop()
			}
			if database != nil {
				return database.Close()
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	var adoptOrigin, forceSweep bool
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile with the ichrisbirch API now",
		Long: "Pushes anything queued, then pulls, regardless of how recently the last pull ran.\n\n" +
			"A pull deletes everything the server did not return, so the database is bound to\n" +
			"the API it was first pulled from and refuses to reconcile against any other.\n" +
			"--adopt rebinds it; --force allows a pull that would delete most of what is here.",
		Example: "  todoui sync            push queued edits, then pull from the API now\n" +
			"  todoui sync --adopt    bind this database to the configured API, then sync\n" +
			"  todoui sync --force    sync even when the pull would delete most local rows",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if syncEngine == nil {
				return fmt.Errorf("sync is disabled — enable it in ~/.config/todoui/config.toml")
			}
			// Named on the way in, not after: --adopt rewrites which API owns
			// this database, and doing that silently is the mistake the binding
			// exists to prevent.
			if adoptOrigin {
				fmt.Printf("Binding %s to %s\n", cfg.Local.DBPath, syncEngine.APIURL())
			}
			if err := syncEngine.PullWith(cmd.Context(), sync.PullOptions{
				Adopt:           adoptOrigin,
				AllowLargeSweep: forceSweep,
			}); err != nil {
				return err
			}
			fmt.Println("Synced.")
			return nil
		},
	}
	syncCmd.Flags().BoolVar(&adoptOrigin, "adopt", false,
		"bind this database to the configured API, replacing whatever it was bound to")
	syncCmd.Flags().BoolVar(&forceSweep, "force", false,
		"allow a pull that would delete most of the local projects or items")
	root.AddCommand(syncCmd, cli.UpdateCmd(), cli.VersionCmd())

	return root
}
