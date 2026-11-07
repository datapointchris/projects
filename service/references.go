package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
)

// ResolveProject answers the project a reference names: its UUID, or its name.
func (s *Service) ResolveProject(ref string) (*model.Project, error) {
	project, err := resolveProject(s.ctx(), s.store, ref)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// ResolveItem answers the item a reference names: its UUID, or its number.
func (s *Service) ResolveItem(ref string) (*model.ProjectItem, error) {
	item, err := resolveItem(s.ctx(), s.store, ref)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func refuse(kind error, format string, args ...any) error {
	return &model.Refusal{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// canonicalUUID reports whether ref parses as a UUID, in the one spelling a store
// holds it in.
func canonicalUUID(ref string) (string, bool) {
	parsed, err := uuid.Parse(ref)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

// resolveProject tells the two forms apart without guessing: a UUID parses as one
// and a name does not.
func resolveProject(ctx context.Context, reader store.Operations, ref string) (model.Project, error) {
	id, isUUID := canonicalUUID(ref)
	if !isUUID {
		return projectByName(ctx, reader, ref)
	}
	project, err := reader.GetProject(ctx, id)
	if errors.Is(err, model.ErrNotFound) {
		return model.Project{}, refuse(model.ErrNotFound, "project %s not found", ref)
	}
	if err != nil {
		return model.Project{}, fmt.Errorf("getting project: %w", err)
	}
	return project, nil
}

// projectByName resolves a name, which is unique only among active projects. The
// active one wins, because the live effort is what a name means while one exists.
// A lone closed match is what makes reopening addressable at all. Several closed
// projects sharing it is ambiguous, and the refusal lists their ids.
func projectByName(ctx context.Context, reader store.Operations, name string) (model.Project, error) {
	projects, err := reader.ListProjects(ctx)
	if err != nil {
		return model.Project{}, fmt.Errorf("listing projects: %w", err)
	}

	var matches []model.Project
	for _, project := range projects {
		if project.Name != name {
			continue
		}
		if project.Status == model.StatusActive {
			return project, nil
		}
		matches = append(matches, project)
	}

	switch len(matches) {
	case 0:
		message := fmt.Sprintf("project %s not found", name)
		if near := namesNear(name, projectNamesActiveFirst(projects)); len(near) > 0 {
			quoted := make([]string, len(near))
			for i, candidate := range near {
				quoted[i] = quoteName(candidate)
			}
			message += ". A name matches only in full, and these come near it: " + strings.Join(quoted, ", ")
		}
		return model.Project{}, &model.Refusal{Kind: model.ErrNotFound, Message: message}
	case 1:
		return matches[0], nil
	}

	slices.SortStableFunc(matches, func(a, b model.Project) int { return strings.Compare(a.Status, b.Status) })
	candidates := make([]string, len(matches))
	for i, project := range matches {
		candidates[i] = fmt.Sprintf("%s (%s)", project.ID, project.Status)
	}
	return model.Project{}, refuse(model.ErrAmbiguousName, "%s names %d closed projects and no active one. Use an id: %s",
		quoteName(name), len(matches), strings.Join(candidates, ", "))
}

// projectNamesActiveFirst keeps the store's board order within each group.
func projectNamesActiveFirst(projects []model.Project) []string {
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		if project.Status == model.StatusActive {
			names = append(names, project.Name)
		}
	}
	for _, project := range projects {
		if project.Status != model.StatusActive {
			names = append(names, project.Name)
		}
	}
	return names
}

// resolveItem parses a UUID before testing for digits, because a hyphenless UUID
// can be 32 decimal digits and no number will ever be that long.
func resolveItem(ctx context.Context, reader store.Operations, ref string) (model.ProjectItem, error) {
	var item model.ProjectItem
	var err error
	if id, isUUID := canonicalUUID(ref); isUUID {
		item, err = reader.GetItem(ctx, id)
	} else if number, isNumber := parseItemNumber(ref); isNumber {
		item, err = reader.GetItemByNumber(ctx, number)
	} else {
		return model.ProjectItem{}, refuse(model.ErrInvalidReference, "%s is not a project item UUID or number", quoteName(ref))
	}
	if errors.Is(err, model.ErrNotFound) {
		return model.ProjectItem{}, refuse(model.ErrNotFound, "project item %s not found", ref)
	}
	if err != nil {
		return model.ProjectItem{}, fmt.Errorf("getting item: %w", err)
	}
	return item, nil
}

func parseItemNumber(ref string) (int, bool) {
	if ref == "" {
		return 0, false
	}
	for _, r := range ref {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(ref)
	return number, err == nil
}

// itemHandle is what a refusal calls an item. An item created while sync was
// unreachable has no number yet, so it goes by its id.
func itemHandle(item model.ProjectItem) string {
	if item.Number != nil {
		return strconv.Itoa(*item.Number)
	}
	return item.ID
}

// quoteName writes a name in a sentence: single-quoted, or double-quoted where it
// holds a single quote and no double, with anything unprintable escaped.
func quoteName(name string) string {
	quote := '\''
	if strings.ContainsRune(name, '\'') && !strings.ContainsRune(name, '"') {
		quote = '"'
	}
	var quoted strings.Builder
	quoted.WriteRune(quote)
	for _, r := range name {
		switch {
		case r == quote || r == '\\':
			quoted.WriteRune('\\')
			quoted.WriteRune(r)
		case r == '\n':
			quoted.WriteString(`\n`)
		case r == '\t':
			quoted.WriteString(`\t`)
		case r == '\r':
			quoted.WriteString(`\r`)
		case !unicode.IsPrint(r):
			fmt.Fprintf(&quoted, `\x%02x`, r)
		default:
			quoted.WriteRune(r)
		}
	}
	quoted.WriteRune(quote)
	return quoted.String()
}

const (
	nearNamesShown     = 5
	nearNameMinimumFit = 0.6
)

// namesNear answers the names a reference that resolved to nothing most likely
// meant. Containment comes first, because it is how a short form misses, and names
// a typo away come after it. The order of names is kept within containment, so a
// caller passing active projects first sees them offered first.
func namesNear(typed string, names []string) []string {
	var folded []string
	original := make(map[string]string, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		if _, seen := original[key]; !seen {
			original[key] = name
			folded = append(folded, key)
		}
	}
	typedFolded := strings.ToLower(typed)

	var ordered []string
	for _, key := range folded {
		if strings.Contains(key, typedFolded) {
			ordered = append(ordered, key)
		}
	}
	for _, key := range closestByFit(typedFolded, folded) {
		if !slices.Contains(ordered, key) {
			ordered = append(ordered, key)
		}
	}
	if len(ordered) > nearNamesShown {
		ordered = ordered[:nearNamesShown]
	}
	near := make([]string, len(ordered))
	for i, key := range ordered {
		near[i] = original[key]
	}
	return near
}

// closestByFit keeps the candidates that fit typed well enough, best first, and on
// a tie the alphabetically later one first.
func closestByFit(typed string, candidates []string) []string {
	type scored struct {
		name string
		fit  float64
	}
	var kept []scored
	for _, candidate := range candidates {
		if fit := matchRatio(candidate, typed); fit >= nearNameMinimumFit {
			kept = append(kept, scored{candidate, fit})
		}
	}
	slices.SortFunc(kept, func(a, b scored) int {
		switch {
		case a.fit > b.fit:
			return -1
		case a.fit < b.fit:
			return 1
		}
		return strings.Compare(b.name, a.name)
	})
	if len(kept) > nearNamesShown {
		kept = kept[:nearNamesShown]
	}
	closest := make([]string, len(kept))
	for i, candidate := range kept {
		closest[i] = candidate.name
	}
	return closest
}

// matchRatio is the Ratcliff-Obershelp similarity of two strings: twice the
// characters in their matching blocks, over their combined length.
func matchRatio(a, b string) float64 {
	left, right := []rune(a), []rune(b)
	if len(left)+len(right) == 0 {
		return 1
	}
	return 2 * float64(matchedRunes(left, right)) / float64(len(left)+len(right))
}

// matchedRunes takes the longest common block, earliest in left and then in right,
// and recurses on what lies either side of it.
func matchedRunes(left, right []rune) int {
	bestLeft, bestRight, bestSize := 0, 0, 0
	previousRun := make([]int, len(right)+1)
	for i, r := range left {
		run := make([]int, len(right)+1)
		for j, candidate := range right {
			if candidate != r {
				continue
			}
			run[j+1] = previousRun[j] + 1
			if run[j+1] > bestSize {
				bestLeft, bestRight, bestSize = i-run[j+1]+1, j-run[j+1]+1, run[j+1]
			}
		}
		previousRun = run
	}
	if bestSize == 0 {
		return 0
	}
	return bestSize +
		matchedRunes(left[:bestLeft], right[:bestRight]) +
		matchedRunes(left[bestLeft+bestSize:], right[bestRight+bestSize:])
}
