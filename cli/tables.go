package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/model"
)

func newTable(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
}

func printProjectsTable(out io.Writer, projects []wire.ProjectWithItemCount, withStatus bool) {
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(out, "No projects yet. Create one with `projects create --name ...`.")
		return
	}
	tw := newTable(out)
	header := []string{"ID", "NAME", "KIND", "OPEN", "DONE", "ITEMS", "POS", "REPOS"}
	if withStatus {
		header = slices.Insert(header, 3, "STATUS")
	}
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, p := range projects {
		fields := []string{p.ID, p.Name, p.Kind}
		if withStatus {
			fields = append(fields, p.Status)
		}
		fields = append(fields, strconv.Itoa(p.OpenCount), strconv.Itoa(p.CompletedCount), strconv.Itoa(p.ItemCount),
			strconv.Itoa(p.Position), repoList(p.Repos))
		_, _ = fmt.Fprintln(tw, strings.Join(fields, "\t"))
	}
	_ = tw.Flush()
}

func printProjectDetail(out io.Writer, p wire.ProjectWithItemCount, items []wire.ProjectItemInProject) {
	_, _ = fmt.Fprintf(out, "%s\n", p.Name)
	_, _ = fmt.Fprintf(out, "  id:        %s\n", p.ID)
	if d := text(p.Description); d != "" {
		_, _ = fmt.Fprintf(out, "  desc:      %s\n", d)
	}
	_, _ = fmt.Fprintf(out, "  kind:      %s\n", p.Kind)
	_, _ = fmt.Fprintf(out, "  status:    %s%s\n", p.Status, closedSuffix(p.Project))
	if reason := text(p.StatusReason); reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:    %s\n", reason)
	}
	_, _ = fmt.Fprintf(out, "  position:  %d\n", p.Position)
	_, _ = fmt.Fprintf(out, "  repos:     %s\n", repoList(p.Repos))
	_, _ = fmt.Fprintf(out, "  items:     %d (%d open, %d done)\n", p.ItemCount, p.OpenCount, p.CompletedCount)

	_, _ = fmt.Fprintf(out, "\nItems (%d):\n", len(items))
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
		return
	}
	printProjectItemsTable(out, items)
}

func printProjectItemsTable(out io.Writer, items []wire.ProjectItemInProject) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "No items.")
		return
	}
	tw := newTable(out)
	_, _ = fmt.Fprintln(tw, "  #\tSTATUS\tTITLE\tID")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", it.Position, it.Status, it.Title, handle(it.Number, it.ID))
	}
	_ = tw.Flush()
}

func printItemsTable(out io.Writer, items []wire.ProjectItem) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "No items.")
		return
	}
	tw := newTable(out)
	_, _ = fmt.Fprintln(tw, "STATUS\tTITLE\tID")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Status, it.Title, handle(it.Number, it.ID))
	}
	_ = tw.Flush()
}

// printQueueTable is the focus project's queue. Every row is open and in the one
// project named above it, so neither a status nor a project column is printed.
func printQueueTable(out io.Writer, items []wire.ProjectItem) {
	tw := newTable(out)
	_, _ = fmt.Fprintln(tw, "TITLE\tID")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", it.Title, handle(it.Number, it.ID))
	}
	_ = tw.Flush()
}

func printItemDetail(out io.Writer, d wire.ProjectItemDetail, tasks []wire.ProjectItemTask, blockers []wire.ProjectItem) {
	_, _ = fmt.Fprintf(out, "%s\n", d.Title)
	_, _ = fmt.Fprintf(out, "  id:       %s\n", handle(d.Number, d.ID))
	_, _ = fmt.Fprintf(out, "  status:   %s\n", d.Status)
	if r := text(d.Repo); r != "" {
		_, _ = fmt.Fprintf(out, "  repo:     %s\n", r)
	}
	if n := text(d.Notes); n != "" {
		_, _ = fmt.Fprintf(out, "  notes:    %s\n", n)
	}
	names := make([]string, 0, len(d.Projects))
	for _, p := range d.Projects {
		names = append(names, p.Name)
	}
	_, _ = fmt.Fprintf(out, "  projects: %s\n", orNone(strings.Join(names, ", ")))

	if len(d.DependsOn) > 0 {
		_, _ = fmt.Fprintf(out, "\nDepends on (%d):\n", len(d.DependsOn))
		for _, dependency := range d.DependsOn {
			_, _ = fmt.Fprintf(out, "  %s %s %s\n", statusMark(dependency.Status), handle(dependency.Number, dependency.ID), dependency.Title)
		}
	}

	_, _ = fmt.Fprintf(out, "\nTasks (%d):\n", len(tasks))
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
	} else {
		printTasksTable(out, tasks)
	}

	if len(blockers) > 0 {
		_, _ = fmt.Fprintf(out, "\nBlocked by (%d):\n", len(blockers))
		for _, b := range blockers {
			_, _ = fmt.Fprintf(out, "  %s %s\n", handle(b.Number, b.ID), b.Title)
		}
	}
}

func printTasksTable(out io.Writer, tasks []wire.ProjectItemTask) {
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(out, "No tasks.")
		return
	}
	tw := newTable(out)
	_, _ = fmt.Fprintln(tw, "  #\tSTATUS\tTITLE\tID")
	for _, t := range tasks {
		_, _ = fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", t.Position, taskState(t), t.Title, t.ID)
	}
	_ = tw.Flush()
}

// taskState is the item vocabulary minus archived, which a task cannot be.
func taskState(t wire.ProjectItemTask) string {
	if t.Completed {
		return model.ItemStatusCompleted
	}
	return model.ItemStatusOpen
}

// statusMark draws an item's status as one character: open ○, completed ✓,
// archived ▪.
func statusMark(status string) string {
	switch status {
	case model.ItemStatusArchived:
		return "▪"
	case model.ItemStatusCompleted:
		return "✓"
	default:
		return "○"
	}
}

// handle is an item's number, or the tail of its id where it has none.
func handle(number *int, id string) string {
	if number != nil {
		return strconv.Itoa(*number)
	}
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// closedSuffix dates a terminal status, and is empty for a project with no close.
func closedSuffix(p wire.Project) string {
	if p.ClosedAt == nil {
		return ""
	}
	return " (" + p.ClosedAt.Local().Format(dayLayout) + ")"
}

// repoList renders a project's derived repos, with a dash where its items touch none.
func repoList(repos []string) string {
	if len(repos) == 0 {
		return "—"
	}
	return strings.Join(repos, ",")
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
