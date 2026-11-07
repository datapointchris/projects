package graph

import "github.com/datapointchris/projects/model"

// ItemNode is an item joined to the one fact a drawing needs beyond the item
// itself: the projects it is filed under, which is what says when a dependency
// leaves the project of the item waiting on it.
type ItemNode struct {
	Item     model.ProjectItem
	Projects []string
}

// BuildItems assembles a tree and its lookup from bulk reads covering the whole
// store. Every caller passes the full item set rather than a filtered one: an
// edge crosses status and project boundaries freely, so a subset severs edges
// and silently splits one tree into several.
func BuildItems(
	items []model.ProjectItem,
	deps []model.Dependency,
	memberships []model.Membership,
	projectNames map[string]string,
) (*Tree, map[string]ItemNode) {
	depsByItem := make(map[string][]string, len(deps))
	for _, d := range deps {
		depsByItem[d.ItemID] = append(depsByItem[d.ItemID], d.DependsOnID)
	}
	namesByItem := make(map[string][]string, len(memberships))
	for _, m := range memberships {
		if name, ok := projectNames[m.ProjectID]; ok {
			namesByItem[m.ItemID] = append(namesByItem[m.ItemID], name)
		}
	}

	nodes := make([]Node, 0, len(items))
	lookup := make(map[string]ItemNode, len(items))
	for _, item := range items {
		nodes = append(nodes, Node{ID: item.ID, Order: itemOrder(item), Deps: depsByItem[item.ID]})
		lookup[item.ID] = ItemNode{Item: item, Projects: namesByItem[item.ID]}
	}
	return BuildTree(nodes), lookup
}

// itemOrder sorts by the item number, the handle a reader recognizes. An item
// created while the API was unreachable has none yet, and sorting those last
// keeps them from jumping to the front of every drawing.
func itemOrder(item model.ProjectItem) int {
	if item.Number == nil {
		return 1 << 30
	}
	return *item.Number
}

// CrossProjectTag names where a dependency lands when it shares no project with
// the item waiting on it. Those edges are the ones neither project's own list
// can show. Empty at a root, and empty when the two ends share any project.
func CrossProjectTag(child, parent ItemNode, hasParent bool) string {
	if !hasParent || len(child.Projects) == 0 {
		return ""
	}
	for _, name := range child.Projects {
		for _, other := range parent.Projects {
			if name == other {
				return ""
			}
		}
	}
	return child.Projects[0]
}

// HasOpenWork reports whether any member of a component is still open, which is
// what decides if a finished tree is drawn. A finished tree is history, and
// they accumulate without bound.
func HasOpenWork(members []string, lookup map[string]ItemNode) bool {
	for _, id := range members {
		item := lookup[id].Item
		if !item.Completed && !item.Archived {
			return true
		}
	}
	return false
}
