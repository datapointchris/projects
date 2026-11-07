package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/model"
)

// defaultNextItemLimit caps the items `projects next` prints; --limit raises it.
const defaultNextItemLimit = 10

// focusShown is `projects next --json`. cli/schemas/focus-queue.schema.json is its
// shape. The totals count what the cap left out.
type focusShown struct {
	SchemaVersion int                `json:"schema_version"`
	Project       *wire.Project      `json:"project"`
	Next          []wire.ProjectItem `json:"next"`
	NextTotal     int                `json:"next_total"`
	Blocked       []wire.ProjectItem `json:"blocked"`
	BlockedTotal  int                `json:"blocked_total"`
}

const focusShownVersion = 1

func newProjectsNextCommand() *cobra.Command {
	var (
		asJSON bool
		kind   string
		repo   string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "The project in focus and its open items, in the order to take them",
		Long: "Projects are worked one at a time. The focus is the highest-ranked active project\n" +
			"holding an open item. It is printed with its items in their position order, so\n" +
			"the first row is the next thing to do. No other project appears until it is\n" +
			"completed or moved down with `projects edit <project> --position <n>`.\n" +
			"\n" +
			"If every open item in the focus project is blocked, it keeps the focus and its\n" +
			"blocked items are listed instead. An item in several projects belongs to the\n" +
			"highest-ranked active one.",
		Example: "  projects next\n" +
			"  projects next --kind chore\n" +
			"  projects next --repo photo-sorter\n" +
			"  projects next --limit 1 --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := repoFlagValue(cmd, repo)
			if err != nil {
				return err
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			// Every open item, uncapped, because --limit caps what is printed here.
			all, err := source.client.ListItems(cmd.Context(), apiclient.ItemQuery{Status: model.ItemStatusOpen, Repo: filter})
			if err != nil {
				return source.explain(err)
			}
			// Blocked is unfiltered: a blocker in another repo still blocks, and
			// filtering would present blocked work as ready.
			blocked, err := source.client.ListBlockedItems(cmd.Context(), nil)
			if err != nil {
				return source.explain(err)
			}
			focus := focusQueue(all, blocked, kind)
			items := capRows(focus.Next, limit)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), focusShown{
					SchemaVersion: focusShownVersion,
					Project:       focus.Project,
					Next:          items,
					NextTotal:     len(focus.Next),
					Blocked:       capRows(focus.Blocked, limit),
					BlockedTotal:  len(focus.Blocked),
				})
			}
			out := cmd.OutOrStdout()
			// The sentences read the uncapped set, so --limit 0 never reports no focus.
			if focus.Project == nil {
				switch {
				case kind != "":
					_, _ = fmt.Fprintf(out, "No open items in %s projects.\n", kind)
				case filter != nil:
					_, _ = fmt.Fprintf(out, "No open items for repo %s.\n", *filter)
				default:
					_, _ = fmt.Fprintln(out, "No items.")
				}
				return nil
			}
			if len(focus.Next) == 0 {
				_, _ = fmt.Fprintf(out, "%s: nothing to take, every open item is blocked.\n", focus.Project.Name)
				printItemsTable(out, focus.Blocked)
				return nil
			}
			// A zero cap prints nothing, the way `head -n 0` does.
			if len(items) == 0 {
				return nil
			}
			_, _ = fmt.Fprintln(out, focus.Project.Name)
			printQueueTable(out, items)
			noteCappedRows(cmd, len(items), len(focus.Next))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the project and its items as JSON to stdout")
	cmd.Flags().StringVar(&kind, "kind", "", "Only projects of this kind: "+strings.Join(model.ProjectKinds, ", "))
	cmd.Flags().StringVar(&repo, "repo", "", "Only items tagged with this repo (empty string for untagged work)")
	addCapFlag(cmd, &limit, defaultNextItemLimit, "Maximum number of items to show")
	return cmd
}

// projectFocus is the one project being worked through and its open items. Next
// can be taken now, and Blocked waits on a dependency, each in the order taken.
// Project is nil when no active project holds an open item.
type projectFocus struct {
	Project *wire.Project
	Next    []wire.ProjectItem
	Blocked []wire.ProjectItem
}

// focusQueue picks the highest-ranked active project holding an open item, and
// returns its open items. A project whose open items are all blocked keeps the
// focus with nothing to take, because handing the focus on would switch work
// without anyone deciding to. A kind narrows which projects rank as well as
// which items count.
func focusQueue(all, blocked []wire.ProjectItem, kind string) projectFocus {
	isBlocked := make(map[string]bool, len(blocked))
	for _, item := range blocked {
		isBlocked[item.ID] = true
	}
	var focus projectFocus
	var open []wire.ProjectItem
	for _, item := range all {
		if item.Completed || item.Archived {
			continue
		}
		project, active := primaryProject(item, kind)
		if !active {
			continue
		}
		if focus.Project == nil || outranks(project, *focus.Project) {
			focus.Project = &project
		}
		open = append(open, item)
	}
	if focus.Project == nil {
		return focus
	}
	sort.SliceStable(open, func(a, b int) bool { return takenBefore(open[a], open[b], kind) })
	for _, item := range open {
		if project, _ := primaryProject(item, kind); project.ID != focus.Project.ID {
			continue
		}
		if isBlocked[item.ID] {
			focus.Blocked = append(focus.Blocked, item)
		} else {
			focus.Next = append(focus.Next, item)
		}
	}
	return focus
}

// takenBefore orders by the rank of each item's project, then its place there.
// Creation time and id settle the rest.
func takenBefore(a, b wire.ProjectItem, kind string) bool {
	projectA, _ := primaryProject(a, kind)
	projectB, _ := primaryProject(b, kind)
	if projectA.ID != projectB.ID {
		return outranks(projectA, projectB)
	}
	if queuedA, queuedB := queuePosition(a, projectA.ID), queuePosition(b, projectB.ID); queuedA != queuedB {
		return queuedA < queuedB
	}
	if !a.CreatedAt.Equal(b.CreatedAt.Time) {
		return a.CreatedAt.Before(b.CreatedAt.Time)
	}
	return a.ID < b.ID
}

func queuePosition(item wire.ProjectItem, projectID string) int {
	for _, membership := range item.Memberships {
		if membership.ProjectID == projectID {
			return membership.Position
		}
	}
	return math.MaxInt
}

// primaryProject is the highest-ranked active project an item is in, of kind
// where one is named, and whether it has one.
func primaryProject(item wire.ProjectItem, kind string) (wire.Project, bool) {
	var primary wire.Project
	found := false
	for _, project := range item.Projects {
		if project.Status != model.StatusActive || (kind != "" && project.Kind != kind) {
			continue
		}
		if !found || outranks(project, primary) {
			primary = project
			found = true
		}
	}
	return primary, found
}

// outranks orders projects by position. Creation time then id break a tie, which
// keeps the order stable between reads.
func outranks(a, b wire.Project) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	if !a.CreatedAt.Equal(b.CreatedAt.Time) {
		return a.CreatedAt.Before(b.CreatedAt.Time)
	}
	return a.ID < b.ID
}
