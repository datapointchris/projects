package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/graph"
	"github.com/datapointchris/projects/model"
)

func newItemDependenciesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "dependencies",
		Short: "Record what an item waits on, and draw the trees that makes",
		Long: "An item depends on another when it cannot finish first. An item with an open\n" +
			"dependency is blocked: `projects items blocked` lists them.",
		Example: "  projects items dependencies add 118 --depends-on 42\n" +
			"  projects items dependencies tree 118",
	})
	goclikit.WithRecoveryHints(cmd, itemHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading, goclikit.WithRecoveryHints(newDependenciesTreeCommand(), itemAndProjectHints...))...)
	cmd.AddCommand(inGroup(groupChanging, newDependenciesAddCommand(), newDependenciesRemoveCommand())...)
	return cmd
}

func newDependenciesAddCommand() *cobra.Command {
	var (
		dependsOn string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:     "add <item> --depends-on <other-item>",
		Short:   "Record that an item depends on another",
		Long:    "An item cannot depend on itself, and a dependency that would close a cycle is\nrefused.",
		Example: "  projects items dependencies add 118 --depends-on 42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			detail, err := source.client.AddDependency(cmd.Context(), args[0], dependsOn)
			if err != nil {
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), detail)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Item %q now depends on %s (%d dependencies)\n", detail.Title, dependsOn, len(detail.DependencyIDs))
			return nil
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Item number or id this item depends on (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the item detail as JSON to stdout")
	return cmd
}

func newDependenciesRemoveCommand() *cobra.Command {
	var (
		dependsOn string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:     "remove <item> --depends-on <other-item>",
		Short:   "Remove a dependency between two items",
		Example: "  projects items dependencies remove 118 --depends-on 42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Remove dependency %s → %s?", args[0], dependsOn))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := source.client.RemoveDependency(cmd.Context(), args[0], dependsOn); err != nil {
				return source.explain(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed dependency %s → %s\n", args[0], dependsOn)
			return nil
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Item number or id to stop depending on (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func newDependenciesTreeCommand() *cobra.Command {
	var (
		asJSON     bool
		invert     bool
		depth      int
		project    string
		repo       string
		itemStatus string
	)
	cmd := &cobra.Command{
		Use:   "tree [<item>]",
		Short: "Draw the dependency trees, or the one an item sits in",
		Long: "Name an item to draw the tree it belongs to, or nothing to draw every tree.\n" +
			"An item with no dependencies either way is in no tree.\n" +
			"\n" +
			"A child is something its parent waits on. --invert reads the edges the other\n" +
			"way, so the children are the work the root unblocks.\n" +
			"\n" +
			"--status, --project and --repo choose which trees are drawn, not which rows\n" +
			"appear in one, because dropping a row orphans everything below it. A tree is\n" +
			"kept when any of its items matches. A tree with no open work is hidden by\n" +
			"default.\n" +
			"\n" +
			"A dependency outside its parent's projects is tagged with the project it is\n" +
			"in. (*) marks an item whose dependencies were already drawn higher up.",
		Example: "  projects items dependencies tree\n" +
			"  projects items dependencies tree 179\n" +
			"  projects items dependencies tree 179 --invert\n" +
			"  projects items dependencies tree --repo photo-sorter\n" +
			"  projects items dependencies tree --status all --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateItemStatus(cmd, itemStatus); err != nil {
				return err
			}
			if depth < 0 {
				return goclikit.UsageError(errors.New("--depth cannot be negative"))
			}
			maxDepth := graph.Unlimited
			if cmd.Flags().Changed("depth") {
				maxDepth = depth
			}
			source, err := openDataSource(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			repoFilter, err := repoFlagValue(cmd, source, repo)
			if err != nil {
				return err
			}
			// Every item, whatever the flags narrow to: a finished item mid-chain
			// joins the halves either side of it, so a subset splits one tree.
			items, err := source.client.ListItems(cmd.Context(), apiclient.ItemQuery{Status: model.StatusAll})
			if err != nil {
				return source.explain(err)
			}
			byID := make(map[string]wire.ProjectItem, len(items))
			nodes := make([]graph.Node, 0, len(items))
			for _, it := range items {
				byID[it.ID] = it
				nodes = append(nodes, graph.Node{ID: it.ID, Order: treeOrder(it.Number), Deps: it.DependencyIDs})
			}
			tree := graph.BuildTree(nodes)

			var drawings []drawing
			if len(args) == 1 {
				item, err := findItem(items, args[0])
				if err != nil {
					return err
				}
				members := tree.ComponentOf(item.ID)
				if members == nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Item %s has no dependencies either way.\n", handle(item.Number, item.ID))
					members = []string{item.ID}
				}
				drawings = []drawing{{roots: []string{item.ID}, members: members}}
			} else {
				for _, members := range tree.Components() {
					if componentMatches(members, byID, itemStatus, project, repoFilter) {
						drawings = append(drawings, drawing{roots: tree.Roots(members, invert), members: members})
					}
				}
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), treeShownOf(tree, byID, drawings, invert, maxDepth))
			}
			printTrees(cmd.OutOrStdout(), tree, byID, drawings, invert, maxDepth)
			if len(args) == 0 && !cmd.Flags().Changed("status") {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nTrees with no open work are hidden: %s --status all\n", cmd.CommandPath())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the nodes and edges as JSON to stdout")
	cmd.Flags().BoolVar(&invert, "invert", false, "Draw what each item unblocks instead of what it waits on")
	cmd.Flags().IntVar(&depth, "depth", 0, "Stop drawing below this many levels (default every level)")
	cmd.Flags().StringVar(&project, "project", "", "Only trees holding an item in this project")
	cmd.Flags().StringVar(&repo, "repo", "", "Only trees holding an item tagged with this repo (empty string for untagged work)")
	cmd.Flags().StringVar(&itemStatus, "status", "", "Keep trees holding an item in this state: "+strings.Join(itemStatuses, ", ")+" (default open)")
	return cmd
}

// treeOrder sorts siblings by number, with an unnumbered item last.
func treeOrder(number *int) int {
	if number == nil {
		return 1 << 30
	}
	return *number
}

// findItem finds the item a reference names among those already read, by number
// or id, so naming the root costs no second request.
func findItem(items []wire.ProjectItem, ref string) (wire.ProjectItem, error) {
	if number, err := strconv.Atoi(ref); err == nil {
		for _, it := range items {
			if it.Number != nil && *it.Number == number {
				return it, nil
			}
		}
	}
	for _, it := range items {
		if it.ID == ref {
			return it, nil
		}
	}
	return wire.ProjectItem{}, fmt.Errorf("no item %q — `projects items list --status all` shows the numbers", ref)
}

// componentMatches keeps a tree when any of its items answers each filter. The
// status default is open, as on every other item read.
func componentMatches(members []string, byID map[string]wire.ProjectItem, itemStatus, project string, repo *string) bool {
	if itemStatus == "" {
		itemStatus = model.ItemStatusOpen
	}
	statusOK, projectOK, repoOK := itemStatus == model.StatusAll, project == "", repo == nil
	for _, id := range members {
		item := byID[id]
		statusOK = statusOK || item.Status == itemStatus
		projectOK = projectOK || slices.ContainsFunc(item.Projects, func(p wire.Project) bool {
			return p.Name == project || p.ID == project
		})
		repoOK = repoOK || text(item.Repo) == *repo
	}
	return statusOK && projectOK && repoOK
}

// drawing is one tree to draw: where the walk starts, and the items in it.
type drawing struct {
	roots   []string
	members []string
}

func printTrees(out io.Writer, tree *graph.Tree, byID map[string]wire.ProjectItem, drawings []drawing, invert bool, maxDepth int) {
	if len(drawings) == 0 {
		_, _ = fmt.Fprintln(out, "No dependency trees.")
		return
	}
	for i, d := range drawings {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		for _, row := range tree.Rows(d.roots, invert, maxDepth) {
			_, _ = fmt.Fprintln(out, treeLine(row, byID))
		}
	}
}

func treeLine(row graph.Row, byID map[string]wire.ProjectItem) string {
	item := byID[row.ID]
	var b strings.Builder
	b.WriteString(graph.Prefix(row))
	b.WriteString(statusMark(item.Status))
	b.WriteString(" ")
	b.WriteString(handle(item.Number, item.ID))
	b.WriteString(" ")
	b.WriteString(item.Title)
	if tag := crossProjectTag(item, byID[row.Parent], row.Parent != ""); tag != "" {
		b.WriteString(" [" + tag + "]")
	}
	if row.Repeated {
		b.WriteString(" (*)")
	}
	return b.String()
}

// crossProjectTag names the project a dependency is in when it shares none with
// the item waiting on it, the edge neither project's own list can show.
func crossProjectTag(item, parent wire.ProjectItem, hasParent bool) string {
	if !hasParent || len(item.Projects) == 0 {
		return ""
	}
	for _, p := range item.Projects {
		for _, q := range parent.Projects {
			if p.ID == q.ID {
				return ""
			}
		}
	}
	return item.Projects[0].Name
}

// treeShown is `items dependencies tree --json`: the nodes and edges, never the
// drawing, so a consumer wanting another shape parses no box-drawing.
// cli/schemas/dependency-tree.schema.json is its shape. An edge runs from an item
// to what it depends on whichever way the drawing ran, and only roots follows
// --invert. Edges and roots name items by id, because an item made on a synced
// database has no number until its create reaches the API.
type treeShown struct {
	SchemaVersion int        `json:"schema_version"`
	Nodes         []treeNode `json:"nodes"`
	Edges         []treeEdge `json:"edges"`
	Roots         []string   `json:"roots"`
}

type treeNode struct {
	ID       string   `json:"id"`
	Number   *int     `json:"number"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Repo     *string  `json:"repo"`
	Projects []string `json:"projects"`
}

type treeEdge struct {
	Item      string `json:"item"`
	DependsOn string `json:"depends_on"`
}

const treeShownVersion = 1

func treeShownOf(tree *graph.Tree, byID map[string]wire.ProjectItem, drawings []drawing, invert bool, maxDepth int) treeShown {
	shown := treeShown{SchemaVersion: treeShownVersion, Nodes: []treeNode{}, Edges: []treeEdge{}, Roots: []string{}}
	for _, d := range drawings {
		drawn := make(map[string]bool)
		for _, row := range tree.Rows(d.roots, invert, maxDepth) {
			drawn[row.ID] = true
		}
		for _, id := range d.members {
			if !drawn[id] {
				continue
			}
			item := byID[id]
			projects := make([]string, 0, len(item.Projects))
			for _, p := range item.Projects {
				projects = append(projects, p.Name)
			}
			shown.Nodes = append(shown.Nodes, treeNode{
				ID: item.ID, Number: item.Number, Title: item.Title, Status: item.Status, Repo: item.Repo, Projects: projects,
			})
			for _, dependency := range item.DependencyIDs {
				if drawn[dependency] {
					shown.Edges = append(shown.Edges, treeEdge{Item: item.ID, DependsOn: dependency})
				}
			}
		}
		shown.Roots = append(shown.Roots, d.roots...)
	}
	return shown
}
