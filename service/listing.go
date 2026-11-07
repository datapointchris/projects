package service

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
)

// withItemCounts adds up each project's memberships.
func withItemCounts(projects []model.Project, facts []model.ProjectItemFact) []model.ProjectWithItemCount {
	counted := make(map[string]*model.ProjectWithItemCount, len(projects))
	repos := make(map[string]map[string]struct{}, len(projects))
	out := make([]model.ProjectWithItemCount, len(projects))
	for i, project := range projects {
		out[i] = model.ProjectWithItemCount{Project: project, Repos: []string{}}
		counted[project.ID] = &out[i]
		repos[project.ID] = map[string]struct{}{}
	}
	for _, fact := range facts {
		project, known := counted[fact.ProjectID]
		if !known {
			continue
		}
		project.ItemCount++
		if fact.Archived {
			continue
		}
		if fact.Completed {
			project.CompletedCount++
		} else {
			project.OpenCount++
		}
		if fact.Repo != nil {
			repos[fact.ProjectID][*fact.Repo] = struct{}{}
		}
	}
	for i := range out {
		for repo := range repos[out[i].ID] {
			out[i].Repos = append(out[i].Repos, repo)
		}
		slices.Sort(out[i].Repos)
	}
	return out
}

func (s *Service) projectsWithItemCounts(ctx context.Context) ([]model.ProjectWithItemCount, []model.ProjectItemFact, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing projects: %w", err)
	}
	facts, err := s.store.ListProjectItemFacts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing item counts: %w", err)
	}
	return withItemCounts(projects, facts), facts, nil
}

// ListProjects is the filtered read. Closed projects come newest close first, and
// projects with no closing date ahead of them, by position.
func (s *Service) ListProjects(filter model.ProjectFilter) ([]model.ProjectWithItemCount, error) {
	ctx := s.ctx()
	if filter.Status != model.StatusAll {
		if err := refuseUnknownStatus(ctx, s.store, filter.Status); err != nil {
			return nil, err
		}
	}
	counted, facts, err := s.projectsWithItemCounts(ctx)
	if err != nil {
		return nil, err
	}

	var holdsRepo map[string]bool
	if filter.Repo != nil {
		holdsRepo = make(map[string]bool)
		for _, fact := range facts {
			if fact.Repo != nil && *fact.Repo == *filter.Repo {
				holdsRepo[fact.ProjectID] = true
			}
		}
	}

	kept := make([]model.ProjectWithItemCount, 0, len(counted))
	for _, project := range counted {
		if filter.Status != model.StatusAll && project.Status != filter.Status {
			continue
		}
		if filter.Repo != nil && !holdsRepo[project.ID] {
			continue
		}
		kept = append(kept, project)
	}
	slices.SortStableFunc(kept, func(a, b model.ProjectWithItemCount) int {
		return cmp.Or(
			model.CompareNewestCloseFirst(a.ClosedAt, b.ClosedAt),
			cmp.Compare(a.Position, b.Position),
			strings.Compare(a.Name, b.Name),
		)
	})
	return capped(kept, filter.Limit), nil
}

func capped[T any](rows []T, limit *int) []T {
	if limit != nil && *limit < len(rows) {
		return rows[:*limit]
	}
	return rows
}

// refuseBadItemFilter names what is wrong with a filter rather than answering it
// with an empty list.
func refuseBadItemFilter(filter model.ItemFilter) error {
	if filter.Since != nil && filter.Until != nil && filter.Since.After(*filter.Until) {
		return refuse(model.ErrInvertedWindow, "since %s is later than until %s",
			isoInstant(*filter.Since), isoInstant(*filter.Until))
	}
	switch filter.Status {
	case model.ItemStatusOpen, model.ItemStatusCompleted, model.ItemStatusArchived, model.StatusAll:
		return nil
	}
	return refuse(model.ErrUnknownItemStatus, "Unknown item status %s. Known statuses: open, completed, archived, all",
		quoteName(filter.Status))
}

func isoInstant(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

func itemPassesFilter(item model.ProjectItem, filter model.ItemFilter) bool {
	if filter.Status != model.StatusAll && item.Status() != filter.Status {
		return false
	}
	if !itemHasRepo(item, filter.Repo) {
		return false
	}
	if filter.Since != nil && (item.CompletedAt == nil || item.CompletedAt.Before(*filter.Since)) {
		return false
	}
	if filter.Until != nil && (item.CompletedAt == nil || !item.CompletedAt.Before(*filter.Until)) {
		return false
	}
	return true
}

// itemHasRepo reads a nil filter as no filter, and an empty one as the untagged
// items, which are a different question from any repo and have to stay askable.
func itemHasRepo(item model.ProjectItem, repo *string) bool {
	switch {
	case repo == nil:
		return true
	case *repo == "":
		return item.Repo == nil
	default:
		return item.Repo != nil && *item.Repo == *repo
	}
}

// ListItems answers the items a filter keeps, newest first, each carrying what is
// filed against it.
func (s *Service) ListItems(filter model.ItemFilter) ([]model.ProjectItemDetail, error) {
	if err := refuseBadItemFilter(filter); err != nil {
		return nil, err
	}
	ctx := s.ctx()
	items, err := s.store.ListAllItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing items: %w", err)
	}
	kept := make([]model.ProjectItem, 0, len(items))
	for _, item := range items {
		if itemPassesFilter(item, filter) {
			kept = append(kept, item)
		}
	}
	return s.withEverythingFiled(ctx, capped(kept, filter.Limit))
}

// ListProjectItems scopes a filter to one project, in project order. A scope
// selects which rows come back and not which states, so the filter means what it
// means in ListItems.
func (s *Service) ListProjectItems(projectRef string, filter model.ItemFilter) ([]model.ProjectItemInProject, error) {
	ctx := s.ctx()
	project, err := resolveProject(ctx, s.store, projectRef)
	if err != nil {
		return nil, err
	}
	if err := refuseBadItemFilter(filter); err != nil {
		return nil, err
	}
	items, err := s.store.ListItemsInProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("listing items in project: %w", err)
	}
	kept := make([]model.ProjectItemInProject, 0, len(items))
	for _, item := range items {
		if itemPassesFilter(item.ProjectItem, filter) {
			kept = append(kept, item)
		}
	}
	return capped(kept, filter.Limit), nil
}

// ListBlockedItems is ListBlocked narrowed to a repo, each item carrying what is
// filed against it.
func (s *Service) ListBlockedItems(repo *string) ([]model.ProjectItemDetail, error) {
	ctx := s.ctx()
	items, err := s.store.ListBlockedItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing blocked: %w", err)
	}
	return s.withEverythingFiled(ctx, keepRepo(items, repo))
}

// SearchItems matches archived items too, narrowed to a repo, each carrying what
// is filed against it.
func (s *Service) SearchItems(query string, repo *string) ([]model.ProjectItemDetail, error) {
	ctx := s.ctx()
	items, err := s.store.SearchItems(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return s.withEverythingFiled(ctx, keepRepo(items, repo))
}

// ListBlockerItems is GetBlockers with each blocker carrying what is filed against it.
func (s *Service) ListBlockerItems(itemRef string) ([]model.ProjectItemDetail, error) {
	blockers, err := s.GetBlockers(itemRef)
	if err != nil {
		return nil, err
	}
	return s.withEverythingFiled(s.ctx(), blockers)
}

func keepRepo(items []model.ProjectItem, repo *string) []model.ProjectItem {
	kept := make([]model.ProjectItem, 0, len(items))
	for _, item := range items {
		if itemHasRepo(item, repo) {
			kept = append(kept, item)
		}
	}
	return kept
}

// withEverythingFiled joins each item to its projects, memberships, dependencies
// and tasks over four flat reads, so a list costs the same however long it is.
func (s *Service) withEverythingFiled(ctx context.Context, items []model.ProjectItem) ([]model.ProjectItemDetail, error) {
	out := make([]model.ProjectItemDetail, len(items))
	if len(items) == 0 {
		return out, nil
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	memberships, err := s.store.ListAllMemberships(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing memberships: %w", err)
	}
	dependencies, err := s.store.ListAllDependencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing dependencies: %w", err)
	}
	tasks, err := s.store.ListAllTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}

	projectByID := make(map[string]model.Project, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	membershipsOf := make(map[string][]model.Membership)
	for _, membership := range memberships {
		membershipsOf[membership.ItemID] = append(membershipsOf[membership.ItemID], membership)
	}
	dependenciesOf := make(map[string][]string)
	for _, dependency := range dependencies {
		dependenciesOf[dependency.ItemID] = append(dependenciesOf[dependency.ItemID], dependency.DependsOnID)
	}
	tasksOf := make(map[string][]model.ProjectItemTask)
	for _, task := range tasks {
		tasksOf[task.ItemID] = append(tasksOf[task.ItemID], task)
	}

	for i, item := range items {
		filed := model.ProjectItemDetail{
			ProjectItem:   item,
			Projects:      []model.Project{},
			Memberships:   orEmpty(membershipsOf[item.ID]),
			DependencyIDs: orEmpty(dependenciesOf[item.ID]),
			Tasks:         orEmpty(tasksOf[item.ID]),
		}
		for _, membership := range filed.Memberships {
			filed.Projects = append(filed.Projects, projectByID[membership.ProjectID])
		}
		sortProjectsByPosition(filed.Projects)
		out[i] = filed
	}
	return out, nil
}

func orEmpty[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}
	return rows
}

func sortProjectsByPosition(projects []model.Project) {
	slices.SortStableFunc(projects, func(a, b model.Project) int {
		return cmp.Or(
			cmp.Compare(a.Position, b.Position),
			a.CreatedAt.Compare(b.CreatedAt),
			strings.Compare(a.ID, b.ID),
		)
	})
}

// refuseUnknownKind reads the lookup table rather than a constant, so adding a kind
// stays an insert.
func refuseUnknownKind(ctx context.Context, reader store.Operations, kind string) error {
	known, err := reader.ListProjectKinds(ctx)
	if err != nil {
		return fmt.Errorf("listing project kinds: %w", err)
	}
	if slices.Contains(known, kind) {
		return nil
	}
	return refuse(model.ErrUnknownKind, "Unknown project kind %s. Known kinds: %s", quoteName(kind), strings.Join(known, ", "))
}

func refuseUnknownStatus(ctx context.Context, reader store.Operations, status string) error {
	known, err := reader.ListProjectStatuses(ctx)
	if err != nil {
		return fmt.Errorf("listing project statuses: %w", err)
	}
	if slices.Contains(known, status) {
		return nil
	}
	return refuse(model.ErrUnknownStatus, "Unknown project status %s. Known statuses: %s, all", quoteName(status), strings.Join(known, ", "))
}
