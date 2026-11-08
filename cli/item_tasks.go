package cli

import (
	"errors"
	"fmt"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/apiclient"
)

func newItemTasksCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "tasks",
		Short: "List and change an item's tasks",
		Long: "A task is a checklist line inside one item. An item with an open task cannot\n" +
			"be completed. <task> is the id `projects items tasks list <item>` prints.",
		Example: "  projects items tasks list 118\n" +
			"  projects items tasks add 118 --title \"Write the tests\"",
	})
	goclikit.WithRecoveryHints(cmd, itemAndTaskHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading, newTasksListCommand())...)
	cmd.AddCommand(inGroup(groupChanging,
		newTasksAddCommand(),
		newTasksCompleteCommand(),
		newTasksEditCommand(),
		newTasksRemoveCommand(),
	)...)
	return cmd
}

func newTasksListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list <item>",
		Short:   "List an item's tasks",
		Example: "  projects items tasks list 118",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			tasks, err := source.client.ListTasks(cmd.Context(), args[0])
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), tasks)
			}
			printTasksTable(cmd.OutOrStdout(), tasks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output tasks as JSON to stdout")
	return cmd
}

func newTasksAddCommand() *cobra.Command {
	var (
		title    string
		position int
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:     "add <item> --title <title>",
		Short:   "Add a task to an item",
		Example: "  projects items tasks add 118 --title \"Write the tests\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return goclikit.UsageError(errors.New("--title is required"))
			}
			input := apiclient.NewTask{Title: title}
			if cmd.Flags().Changed("position") {
				input.Position = &position
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			task, err := source.client.CreateTask(cmd.Context(), args[0], input)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), task)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added task %q (%s)\n", task.Title, task.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
	cmd.Flags().IntVar(&position, "position", 0, "Task position within the item")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the created task as JSON to stdout")
	return cmd
}

func newTasksCompleteCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "complete <item> <task>",
		Short:   "Mark an item's task completed",
		Example: "  projects items tasks complete 118 019a2b3c-…",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			completed := true
			return runTaskUpdate(cmd, args[0], args[1], apiclient.TaskChanges{Completed: &completed}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated task as JSON to stdout")
	return cmd
}

func newTasksEditCommand() *cobra.Command {
	var (
		title     string
		completed bool
		position  int
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:     "edit <item> <task> [flags]",
		Short:   "Change a task's title, completion or position",
		Example: "  projects items tasks edit 118 019a2b3c-… --title \"Write the tests first\"\n  projects items tasks edit 118 019a2b3c-… --completed=false",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			edit := apiclient.TaskChanges{}
			if f.Changed("title") {
				edit.Title = &title
			}
			if f.Changed("completed") {
				edit.Completed = &completed
			}
			if f.Changed("position") {
				edit.Position = &position
			}
			if edit == (apiclient.TaskChanges{}) {
				return goclikit.UsageError(errors.New("nothing to change — pass --title, --completed or --position"))
			}
			return runTaskUpdate(cmd, args[0], args[1], edit, asJSON)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New task title")
	cmd.Flags().BoolVar(&completed, "completed", false, "Set completion: --completed or --completed=false")
	cmd.Flags().IntVar(&position, "position", 0, "New position")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the updated task as JSON to stdout")
	return cmd
}

func runTaskUpdate(cmd *cobra.Command, itemRef, taskID string, edit apiclient.TaskChanges, asJSON bool) error {
	source, err := openForWriting(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	task, err := source.client.UpdateTask(cmd.Context(), itemRef, taskID, edit)
	if err != nil {
		return source.explain(err)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), task)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated task %q → %s (%s)\n", task.Title, taskState(task), task.ID)
	return nil
}

func newTasksRemoveCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <item> <task>",
		Short:   "Delete a task from an item",
		Example: "  projects items tasks remove 118 019a2b3c-… --yes",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete task %s?", args[1]))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := source.client.DeleteTask(cmd.Context(), args[0], args[1]); err != nil {
				return source.explain(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted task %s\n", args[1])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}
