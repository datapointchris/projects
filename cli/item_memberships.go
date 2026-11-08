package cli

import (
	"errors"
	"fmt"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
)

func newItemMembershipsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "memberships",
		Short: "Add an item to another project, or take it out of one",
		Long: "An item is in one or more projects, and holds a place in each. It is always\n" +
			"in at least one, so leaving its last project is refused: delete the item\n" +
			"instead.",
		Example: "  projects items memberships add 118 --project \"Kitchen remodel\"",
	})
	goclikit.WithRecoveryHints(cmd, itemAndProjectHints...)
	cmd.AddCommand(newMembershipsAddCommand(), newMembershipsRemoveCommand())
	return cmd
}

func newMembershipsAddCommand() *cobra.Command {
	var (
		project string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:     "add <item> --project <project>",
		Short:   "Add an item to another project",
		Example: "  projects items memberships add 118 --project \"Kitchen remodel\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return goclikit.UsageError(errors.New("--project is required"))
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			joined, err := source.client.AddItemToProject(cmd.Context(), args[0], project)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), joined)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added item %s to project %q (%s)\n", args[0], joined.Name, joined.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project name or id to add the item to (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the project as JSON to stdout")
	return cmd
}

func newMembershipsRemoveCommand() *cobra.Command {
	var (
		project string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:     "remove <item> --project <project>",
		Short:   "Take an item out of a project",
		Example: "  projects items memberships remove 118 --project \"Kitchen remodel\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return goclikit.UsageError(errors.New("--project is required"))
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Remove item %s from project %s?", args[0], shellQuote(project)))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := source.client.RemoveItemFromProject(cmd.Context(), args[0], project); err != nil {
				return source.explain(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed item %s from project %s\n", args[0], shellQuote(project))
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project name or id to take the item out of (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}
