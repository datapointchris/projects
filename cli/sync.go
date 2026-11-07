package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/config"
	"github.com/datapointchris/projects/sync"
)

func newSyncCommand() *cobra.Command {
	var adoptOrigin, forceSweep bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile a synced database with its API now",
		Long: "For a database with [sync] enabled = true in the config file. `projects tui` on\n" +
			"such a database pushes each edit as it is made and pulls on every interval, so\n" +
			"this is for when the API's state is wanted now: it pushes anything queued, then\n" +
			"pulls.\n" +
			"\n" +
			"A pull deletes everything the server did not return, so the database is bound to\n" +
			"the API it was first pulled from and refuses to reconcile against any other.\n" +
			"--adopt rebinds it; --force allows a pull that would delete most of what is here.\n" +
			"\n" +
			"With api in the config file there is no copy to reconcile, and this refuses.",
		Example: "  projects sync            push queued edits, then pull from the API now\n" +
			"  projects sync --adopt    bind this database to the configured API, then sync\n" +
			"  projects sync --force    sync even when the pull would delete most local rows",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := config.RefuseConfigUnderFormerName(); err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.API != "" {
				return fmt.Errorf("api in %s sends every command to %s, so there is no database copy to sync",
					homeAsTilde(configFilePath()), cfg.API)
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			if source.engine == nil {
				return errors.New("sync is disabled — enable it under [sync] in " + homeAsTilde(configFilePath()))
			}
			// Named on the way in: --adopt rewrites which API owns this database,
			// and doing that silently is the mistake the binding exists to prevent.
			if adoptOrigin {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Binding %s to %s\n", source.cfg.Local.DBPath, source.engine.APIURL())
			}
			if err := source.engine.PullWith(cmd.Context(), sync.PullOptions{
				Adopt:           adoptOrigin,
				AllowLargeSweep: forceSweep,
			}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Synced.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&adoptOrigin, "adopt", false,
		"bind this database to the configured API, replacing whatever it was bound to")
	cmd.Flags().BoolVar(&forceSweep, "force", false,
		"allow a pull that would delete most of the local projects or items")
	return cmd
}
