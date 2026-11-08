package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/model"
)

func newProjectsListCommand() *cobra.Command {
	var (
		asJSON        bool
		repo          string
		projectStatus string
		limit         int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the active projects",
		Long: "The active projects, with the repos their items touch. --repo narrows to the\n" +
			"projects holding work on one repo. Shelved, completed and dropped projects are\n" +
			"hidden until --status asks for them.\n" +
			"\n" +
			"--limit caps what the filters left, so it takes the first projects in\n" +
			"position order rather than filtering a capped slice.",
		Example: "  projects list\n  projects list --repo photo-sorter\n" +
			"  projects list --limit 5\n" +
			"  projects list --status completed\n  projects list --status all --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateStatus(cmd, projectStatus, projectStatuses); err != nil {
				return err
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			filter, err := repoFlagValue(cmd, source, repo)
			if err != nil {
				return err
			}
			projects, err := source.client.ListProjects(cmd.Context(),
				apiclient.ProjectQuery{Status: projectStatus, Repo: filter, Limit: limitFlag(cmd)})
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), projects)
			}
			// "No projects yet, create one" is true of an empty database and false of a
			// filter that matched nothing.
			out := cmd.OutOrStdout()
			switch {
			case len(projects) == 0 && filter != nil:
				_, _ = fmt.Fprintf(out, "No projects hold work on repo %s.\n", *filter)
				return nil
			case len(projects) == 0 && projectStatus != "" && projectStatus != model.StatusAll:
				_, _ = fmt.Fprintf(out, "No %s projects.\n", projectStatus)
				return nil
			}
			// The status column earns its width only where the rows can differ in it.
			printProjectsTable(out, projects, projectStatus != "")
			if projectStatus == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\nShelved, completed and dropped projects are hidden: projects list --status all")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output projects as JSON to stdout")
	cmd.Flags().StringVar(&repo, "repo", "", "Only projects holding work on this repo")
	cmd.Flags().StringVar(&projectStatus, "status", "", "One of: "+strings.Join(projectStatuses, ", ")+" (default active)")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newProjectsShowCommand() *cobra.Command {
	var (
		asJSON     bool
		itemStatus string
	)
	cmd := &cobra.Command{
		Use:     "show <project>",
		Short:   "Show a project and its open items",
		Example: "  projects show \"Kitchen remodel\"\n  projects show \"Kitchen remodel\" --status all --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateItemStatus(cmd, itemStatus); err != nil {
				return err
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			project, err := source.client.GetProject(cmd.Context(), args[0])
			if err != nil {
				return source.explain(err)
			}
			items, err := source.client.ListProjectItems(cmd.Context(), args[0], apiclient.ItemQuery{Status: itemStatus})
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), projectShown{
					SchemaVersion:        projectShownVersion,
					ProjectWithItemCount: project,
					Items:                items,
				})
			}
			printProjectDetail(cmd.OutOrStdout(), project, items)
			hintHiddenItems(cmd, asJSON)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the project and items as JSON to stdout")
	cmd.Flags().StringVar(&itemStatus, "status", "", "One of: "+strings.Join(itemStatuses, ", ")+" (default open)")
	return cmd
}

// projectShown is `projects show --json`: the project's own fields, then its items.
// cli/schemas/project-with-items.schema.json is its shape.
type projectShown struct {
	SchemaVersion int `json:"schema_version"`
	wire.ProjectWithItemCount
	Items []wire.ProjectItemInProject `json:"items"`
}

const projectShownVersion = 1

func newProjectsCreateCommand() *cobra.Command {
	var (
		name        string
		description string
		kind        string
		position    int
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> [flags]",
		Short: "Create a new project",
		Long: "A project names bounded work, something that ends. With no api in the config\n" +
			"file, a name the repo registry knows is refused, because a repo never ends:\n" +
			"tag the items with --repo instead.",
		Example: "  projects create --name \"Kitchen remodel\"\n" +
			"  projects create --name \"Sell unused gear\" --kind chore\n" +
			"  projects create --name \"Learn to sail\" --description \"$(cat plan.md)\" --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return goclikit.UsageError(errors.New("--name is required"))
			}
			input := apiclient.NewProject{Name: name}
			if cmd.Flags().Changed("description") {
				input.Description = &description
			}
			if cmd.Flags().Changed("kind") {
				input.Kind = &kind
			}
			if cmd.Flags().Changed("position") {
				input.Position = &position
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			project, err := source.client.CreateProject(cmd.Context(), input)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), project)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created project %q (%s)\n", project.Name, project.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Project name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Project description")
	cmd.Flags().StringVar(&kind, "kind", "", "What sort of work this is: "+strings.Join(model.ProjectKinds, ", ")+" (default "+model.KindLife+")")
	cmd.Flags().IntVar(&position, "position", 0, "Sort position among projects")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the created project as JSON to stdout")
	return cmd
}

func newProjectsEditCommand() *cobra.Command {
	var (
		name        string
		description string
		kind        string
		position    int
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <project> [flags]",
		Short: "Change fields on an existing project",
		Long: "Updates only the fields whose flags you pass. --description \"\" clears the\n" +
			"description. A status is changed by shelve, complete, close and reopen.",
		Example: "  projects edit \"Kitchen remodel\" --name \"Bathroom remodel\" --position 2\n" +
			"  projects edit \"Kitchen remodel\" --kind chore",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			edit := apiclient.ProjectChanges{}
			if f.Changed("name") {
				edit.Name = &name
			}
			if f.Changed("description") {
				edit.Description = &description
				edit.ClearDescription = description == ""
			}
			if f.Changed("kind") {
				edit.Kind = &kind
			}
			if f.Changed("position") {
				edit.Position = &position
			}
			if edit == (apiclient.ProjectChanges{}) {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --name, --description, --kind, --position"))
			}
			return runProjectUpdate(cmd, args[0], edit, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New project name")
	cmd.Flags().StringVar(&description, "description", "", "New project description")
	cmd.Flags().StringVar(&kind, "kind", "", "New kind: "+strings.Join(model.ProjectKinds, ", "))
	cmd.Flags().IntVar(&position, "position", 0, "New sort position")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated project as JSON to stdout")
	return cmd
}

// The four status verbs are each a PATCH of status, so one write path validates
// every transition and derives closed_at from it.
func newProjectsShelveCommand() *cobra.Command {
	return newProjectStatusCommand(statusVerb{
		use:   "shelve <project>",
		short: "Set a project aside for someday, which hides it",
		long: "For a project you still mean to do, just not now. It moves to --status\n" +
			"someday, out of the active list, and no close is recorded. Its items keep\n" +
			"their state. One in no other active project leaves `projects next` until this\n" +
			"one returns. It also gives up its name, so a new project may take it, and\n" +
			"reopening this one is then refused until one of the two is renamed.",
		example: "  projects shelve \"Garden beds\"",
		status:  model.StatusSomeday,
		done:    "Shelved",
	})
}

func newProjectsCompleteCommand() *cobra.Command {
	return newProjectStatusCommand(statusVerb{
		use:   "complete <project>",
		short: "Mark a project finished, which hides it",
		long: "A project is a finite effort with a definition of done, so completing it is\n" +
			"what takes it out of the list. Its items are left exactly as they are: an item\n" +
			"still open when the project finished was still open, and that is worth knowing.",
		example: "  projects complete \"Kitchen remodel\"",
		status:  model.StatusCompleted,
		done:    "Completed",
	})
}

func newProjectsCloseCommand() *cobra.Command {
	return newProjectStatusCommand(statusVerb{
		use:   "close <project> --reason <why>",
		short: "Close a project you are not going to do",
		long: "For a project that ends without being finished. Its status becomes dropped.\n" +
			"--reason is required: \"deferred\" invites the same idea back next month, and\n" +
			"closed-and-here-is-why settles it.",
		example: "  projects close \"Rewrite in Rust\" --reason \"Go covers it and I know Go\"",
		status:  model.StatusDropped,
		done:    "Closed",
		reason:  true,
	})
}

func newProjectsReopenCommand() *cobra.Command {
	return newProjectStatusCommand(statusVerb{
		use:   "reopen <project>",
		short: "Return a shelved or closed project to the active list",
		long: "Clears the closing reason and date along with the status. Refused if an\n" +
			"active project has taken the name in the meantime, because only the live\n" +
			"project holds a name.",
		example: "  projects reopen \"Kitchen remodel\"",
		status:  model.StatusActive,
		done:    "Reopened",
	})
}

type statusVerb struct {
	use, short, long, example string
	status, done              string
	reason                    bool
}

func newProjectStatusCommand(verb statusVerb) *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:     verb.use,
		Short:   verb.short,
		Long:    verb.long,
		Example: verb.example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := verb.status
			edit := apiclient.ProjectChanges{Status: &status, ClearStatusReason: true}
			if verb.reason {
				if strings.TrimSpace(reason) == "" {
					return goclikit.UsageError(errors.New("--reason is required — say why it is closed rather than shelved"))
				}
				edit.StatusReason, edit.ClearStatusReason = &reason, false
			}
			return runProjectUpdate(cmd, args[0], edit, asJSON, verb.done)
		},
	}
	if verb.reason {
		cmd.Flags().StringVar(&reason, "reason", "", "Why it is closed rather than shelved (required)")
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated project as JSON to stdout")
	return cmd
}

func runProjectUpdate(cmd *cobra.Command, ref string, edit apiclient.ProjectChanges, asJSON bool, done string) error {
	source, err := openForWriting(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	project, err := source.client.UpdateProject(cmd.Context(), ref, edit)
	if err != nil {
		return source.explain(err)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), project)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s project %q (%s)\n", done, project.Name, project.ID)
	return nil
}

func newProjectsDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <project>",
		Short: "Delete a project",
		Long: "Deletes a project for good. Completed items in no other project go with it.\n" +
			"An open item that would be left in no project refuses the delete, so move it\n" +
			"first. Asks to confirm unless --yes.",
		Example: "  projects delete \"Kitchen remodel\"\n  projects delete \"Kitchen remodel\" --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			// Read first, so the question names the project it destroys.
			project, err := source.client.GetProject(cmd.Context(), args[0])
			if err != nil {
				return source.explain(err)
			}
			if !yes {
				question := fmt.Sprintf("Delete project %q (%d items)? This cannot be undone.", project.Name, project.ItemCount)
				ok, err := confirm(cmd, question)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := source.client.DeleteProject(cmd.Context(), project.ID); err != nil {
				return source.explain(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %q (%s)\n", project.Name, project.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

// encodeJSON writes v as indented JSON, the shape every --json prints.
func encodeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
