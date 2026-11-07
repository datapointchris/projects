package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/apiclient"
)

// itemHints and the lists built on it name the commands that find a valid
// reference for each thing an items verb takes.
var (
	itemHints = []string{
		"Search items by title or notes: projects items search <query>",
		"Completed and archived items are hidden: projects items list --status all",
	}
	itemAndProjectHints = append(slices.Clone(itemHints), projectHints...)
	itemAndTaskHints    = append(slices.Clone(itemHints), "List an item's tasks: projects items tasks list <item>")
)

func newItemsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "items",
		Short: "Work with the items that finish a project",
		Long: "An item is one step of a project, with tasks, dependencies on other items,\n" +
			"and a place in one or more projects. <item> is an item's number.\n" +
			"\n" +
			"The nouns under items group what an item holds: `tasks`, `dependencies` and\n" +
			"`memberships`, each with its own verbs.",
		Example: "  projects items list                                every open item, newest first\n" +
			"  projects items list --project \"Kitchen remodel\"    one project's items in order\n" +
			"  projects items show 42                             one item with its tasks and blockers",
	})
	goclikit.WithRecoveryHints(cmd, itemHints...)
	splitReadingFromChanging(cmd)
	cmd.AddGroup(&cobra.Group{ID: groupItems, Title: "What an item holds:"})
	cmd.AddCommand(inGroup(groupReading,
		goclikit.WithRecoveryHints(newItemsListCommand(), itemAndProjectHints...),
		newItemsBlockedCommand(),
		newItemsSearchCommand(),
		newItemsShowCommand(),
		newItemsBlockersCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newItemsCreateCommand(), itemAndProjectHints...),
		newItemsEditCommand(),
		newItemStateCommand("complete", "Mark an item completed", "Completed", itemCompleted(true)),
		newItemStateCommand("reopen", "Reopen a completed item", "Reopened", itemCompleted(false)),
		newItemStateCommand("archive", "Archive an item, which hides it", "Archived", itemArchived(true)),
		newItemStateCommand("unarchive", "Restore an archived item", "Restored", itemArchived(false)),
		newItemsDeleteCommand(),
		goclikit.WithRecoveryHints(newItemsReorderCommand(), itemAndProjectHints...),
	)...)
	cmd.AddCommand(inGroup(groupItems,
		newItemTasksCommand(),
		newItemDependenciesCommand(),
		newItemMembershipsCommand(),
	)...)
	return cmd
}

func newItemsListCommand() *cobra.Command {
	var (
		asJSON     bool
		project    string
		repo       string
		itemStatus string
		limit      int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List open items, or one project's items in order",
		Long: "Open means not completed and not archived. It is the default because\n" +
			"completed items accumulate without bound.\n" +
			"\n" +
			"--status takes one of: " + strings.Join(itemStatuses, ", ") + ". They partition every\n" +
			"item, because archived beats completed.\n" +
			"\n" +
			"--project selects which rows come back, not which states, so it takes the same\n" +
			"--status and defaults to open the same way.\n" +
			"\n" +
			"--since and --until bound when an item was finished. An item never completed has\n" +
			"no such date, so pair them with --status completed to read a week's work.\n" +
			"\n" +
			"--limit caps what the filters left: newest first unscoped, project order under\n" +
			"--project.",
		Example: "  projects items list\n" +
			"  projects items list --status completed --since 2026-08-17\n" +
			"  projects items list --status all --json\n" +
			"  projects items list --repo photo-sorter --limit 10\n" +
			"  projects items list --repo \"\"\n" +
			"  projects items list --project \"Kitchen remodel\" --status all",
		Args: argumentBelongsTo("--project", "One project's items"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateItemStatus(cmd, itemStatus); err != nil {
				return err
			}
			since, until, err := readWindow(cmd)
			if err != nil {
				return err
			}
			if project != "" && cmd.Flags().Changed("repo") {
				return goclikit.UsageError(errors.New("--repo and --project are different questions — pass one"))
			}
			filter, err := repoFlagValue(cmd, repo)
			if err != nil {
				return err
			}
			query := apiclient.ItemQuery{Status: itemStatus, Repo: filter, Since: since, Until: until, Limit: limitFlag(cmd)}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			if project == "" {
				items, err := source.client.ListItems(cmd.Context(), query)
				if err != nil {
					return source.explain(err)
				}
				if asJSON {
					return encodeJSON(cmd.OutOrStdout(), items)
				}
				printItemsTable(cmd.OutOrStdout(), items)
			} else {
				items, err := source.client.ListProjectItems(cmd.Context(), project, query)
				if err != nil {
					return source.explain(err)
				}
				if asJSON {
					return encodeJSON(cmd.OutOrStdout(), items)
				}
				printProjectItemsTable(cmd.OutOrStdout(), items)
			}
			hintHiddenItems(cmd, asJSON)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output items as JSON to stdout")
	cmd.Flags().StringVar(&project, "project", "", "Only one project's items, in project order (full name or id)")
	cmd.Flags().StringVar(&repo, "repo", "", "Only items tagged with this repo (empty string for untagged work)")
	cmd.Flags().StringVar(&itemStatus, "status", "", "One of: "+strings.Join(itemStatuses, ", ")+" (default open)")
	addWindowFlags(cmd, "items completed")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newItemsBlockedCommand() *cobra.Command {
	var (
		asJSON bool
		repo   string
	)
	cmd := &cobra.Command{
		Use:     "blocked",
		Short:   "List items with at least one incomplete dependency",
		Example: "  projects items blocked\n  projects items blocked --repo photo-sorter",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := repoFlagValue(cmd, repo)
			if err != nil {
				return err
			}
			return listItems(cmd, asJSON, func(ctx context.Context, c *apiclient.Client) ([]wire.ProjectItem, error) {
				return c.ListBlockedItems(ctx, filter)
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output items as JSON to stdout")
	cmd.Flags().StringVar(&repo, "repo", "", "Only items tagged with this repo (empty string for untagged work)")
	return cmd
}

func newItemsSearchCommand() *cobra.Command {
	var (
		asJSON bool
		repo   string
	)
	cmd := &cobra.Command{
		Use:     "search <query>",
		Short:   "Search items by title or notes",
		Long:    "The query matches as one piece of text anywhere in an item's title or notes.",
		Example: "  projects items search kitchen\n  projects items search \"off-site backup\" --repo photo-sorter",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := repoFlagValue(cmd, repo)
			if err != nil {
				return err
			}
			return listItems(cmd, asJSON, func(ctx context.Context, c *apiclient.Client) ([]wire.ProjectItem, error) {
				return c.SearchItems(ctx, args[0], filter)
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output items as JSON to stdout")
	cmd.Flags().StringVar(&repo, "repo", "", "Only items tagged with this repo (empty string for untagged work)")
	return cmd
}

func newItemsBlockersCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "blockers <item>",
		Short:   "List the incomplete dependencies blocking an item",
		Example: "  projects items blockers 118",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return listItems(cmd, asJSON, func(ctx context.Context, c *apiclient.Client) ([]wire.ProjectItem, error) {
				return c.ListBlockers(ctx, args[0])
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output blockers as JSON to stdout")
	return cmd
}

// listItems is the body of blocked, search and blockers: one read, then JSON or
// a table.
func listItems(cmd *cobra.Command, asJSON bool, read func(context.Context, *apiclient.Client) ([]wire.ProjectItem, error)) error {
	source, err := openDataSource(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	items, err := read(cmd.Context(), source.client)
	if err != nil {
		return source.explain(err)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), items)
	}
	printItemsTable(cmd.OutOrStdout(), items)
	return nil
}

// itemShown is `items show --json`: the item's own fields, then its tasks and
// what blocks it. cli/schemas/item-with-tasks-and-blockers.schema.json is its shape.
type itemShown struct {
	SchemaVersion int `json:"schema_version"`
	wire.ProjectItemDetail
	Tasks    []wire.ProjectItemTask `json:"tasks"`
	Blockers []wire.ProjectItem     `json:"blockers"`
}

const itemShownVersion = 1

func newItemsShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <item>",
		Short: "Show an item with its projects, dependencies, tasks and blockers",
		Long: "\"Depends on\" lists every item this one waits on, marked open (○), completed (✓)\n" +
			"or archived (▪). \"Blocked by\" is the ones not yet completed. Under --json they\n" +
			"are depends_on and blockers.\n" +
			"\n" +
			"`projects items dependencies tree <item>` draws the whole graph it sits in.",
		Example: "  projects items show 118\n  projects items show 118 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			ctx := cmd.Context()
			detail, err := source.client.GetItem(ctx, args[0])
			if err != nil {
				return source.explain(err)
			}
			tasks, err := source.client.ListTasks(ctx, detail.ID)
			if err != nil {
				return source.explain(err)
			}
			blockers, err := source.client.ListBlockers(ctx, detail.ID)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), itemShown{
					SchemaVersion:     itemShownVersion,
					ProjectItemDetail: detail,
					Tasks:             tasks,
					Blockers:          blockers,
				})
			}
			printItemDetail(cmd.OutOrStdout(), detail, tasks, blockers)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output everything as JSON to stdout")
	return cmd
}

func newItemsEditCommand() *cobra.Command {
	var (
		title  string
		notes  string
		repo   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "edit <item> [flags]",
		Short: "Change an item's title, notes or repo",
		Long: "Updates only the fields whose flags you pass. --notes \"\" clears the notes, and\n" +
			"--repo \"\" unlinks the item from its repo. complete, reopen, archive and\n" +
			"unarchive change its state.",
		Example: "  projects items edit 118 --notes \"$(cat notes.md)\"\n" +
			"  projects items edit 118 --title \"New title\"\n" +
			"  projects items edit 118 --repo photo-sorter --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			edit := apiclient.ItemChanges{}
			if f.Changed("title") {
				edit.Title = &title
			}
			if f.Changed("notes") {
				edit.Notes, edit.ClearNotes = &notes, notes == ""
			}
			if f.Changed("repo") {
				if err := validateRepoFlag(cmd, repo); err != nil {
					return err
				}
				edit.Repo, edit.ClearRepo = &repo, repo == ""
			}
			if edit == (apiclient.ItemChanges{}) {
				return goclikit.UsageError(errors.New("nothing to change — pass --title, --notes or --repo"))
			}
			return runItemUpdate(cmd, args[0], edit, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New item title")
	cmd.Flags().StringVar(&notes, "notes", "", "New markdown notes (empty string clears them)")
	cmd.Flags().StringVar(&repo, "repo", "", "Repo this item is work on, by registry name (empty string unlinks)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated item as JSON to stdout")
	return cmd
}

func itemCompleted(completed bool) apiclient.ItemChanges {
	return apiclient.ItemChanges{Completed: &completed}
}

func itemArchived(archived bool) apiclient.ItemChanges {
	return apiclient.ItemChanges{Archived: &archived}
}

// newItemStateCommand is one of complete, reopen, archive and unarchive, each a
// PATCH of one flag.
func newItemStateCommand(verb, short, done string, edit apiclient.ItemChanges) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     verb + " <item>",
		Short:   short,
		Example: "  projects items " + verb + " 118",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runItemUpdate(cmd, args[0], edit, asJSON, done)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated item as JSON to stdout")
	return cmd
}

func runItemUpdate(cmd *cobra.Command, ref string, edit apiclient.ItemChanges, asJSON bool, done string) error {
	source, err := openForWriting(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	item, err := source.client.UpdateItem(cmd.Context(), ref, edit)
	if err != nil {
		return source.explain(err)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), item)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s item %q → %s (%s)\n", done, item.Title, item.Status, handle(item.Number, item.ID))
	return nil
}

func newItemsDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <item>",
		Short:   "Delete an item",
		Long:    "Deletes an item and its tasks for good. Asks to confirm unless --yes.",
		Example: "  projects items delete 118 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			item, err := source.client.GetItem(cmd.Context(), args[0])
			if err != nil {
				return source.explain(err)
			}
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete item %q? This cannot be undone.", item.Title))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := source.client.DeleteItem(cmd.Context(), item.ID); err != nil {
				return source.explain(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted item %q (%s)\n", item.Title, handle(item.Number, item.ID))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func newItemsReorderCommand() *cobra.Command {
	var (
		project  string
		position int
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:     "reorder <item> --project <project> --position <n>",
		Short:   "Move an item to a new position within a project",
		Example: "  projects items reorder 118 --project \"Kitchen remodel\" --position 2",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return goclikit.UsageError(errors.New("--project is required"))
			}
			if !cmd.Flags().Changed("position") {
				return goclikit.UsageError(errors.New("--position is required"))
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			item, err := source.client.ReorderItem(cmd.Context(), args[0], project, position)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), item)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Moved item %q to position %d (%s)\n", item.Title, item.Position, handle(item.Number, item.ID))
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project name or id the item moves within (required)")
	cmd.Flags().IntVar(&position, "position", 0, "New position (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the item as JSON to stdout")
	return cmd
}
