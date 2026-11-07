package tui

import (
	"fmt"
	"strings"

	"github.com/datapointchris/projects/model"
)

type pickerProject struct {
	id       string
	name     string
	selected bool
	original bool // initial state, for diffing in manage mode
}

type projectPicker struct {
	projects  []pickerProject
	cursor    int
	intent    pickerIntent
	itemID    string
	itemTitle string
}

func newPickerForCreate(allProjects []model.ProjectWithItemCount, currentProjectID string, title string) projectPicker {
	pp := make([]pickerProject, len(allProjects))
	for i, p := range allProjects {
		pp[i] = pickerProject{
			id:       p.ID,
			name:     p.Name,
			selected: p.ID == currentProjectID,
		}
	}
	return projectPicker{
		projects:  pp,
		intent:    pickerCreate,
		itemTitle: title,
	}
}

func newPickerForManage(allProjects []model.ProjectWithItemCount, currentProjects []model.Project, item model.ProjectItem) projectPicker {
	memberSet := make(map[string]bool)
	for _, p := range currentProjects {
		memberSet[p.ID] = true
	}
	pp := make([]pickerProject, len(allProjects))
	for i, p := range allProjects {
		isMember := memberSet[p.ID]
		pp[i] = pickerProject{
			id:       p.ID,
			name:     p.Name,
			selected: isMember,
			original: isMember,
		}
	}
	return projectPicker{
		projects:  pp,
		intent:    pickerManage,
		itemID:    item.ID,
		itemTitle: item.Title,
	}
}

func (p *projectPicker) toggle() {
	if len(p.projects) == 0 {
		return
	}
	cur := &p.projects[p.cursor]
	if cur.selected && p.selectedCount() <= 1 {
		return // must belong to at least 1 project
	}
	cur.selected = !cur.selected
}

func (p *projectPicker) up() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *projectPicker) down() {
	if p.cursor < len(p.projects)-1 {
		p.cursor++
	}
}

func (p *projectPicker) selectedCount() int {
	n := 0
	for _, proj := range p.projects {
		if proj.selected {
			n++
		}
	}
	return n
}

func (p *projectPicker) selectedIDs() []string {
	var ids []string
	for _, proj := range p.projects {
		if proj.selected {
			ids = append(ids, proj.id)
		}
	}
	return ids
}

func (p *projectPicker) toAdd() []string {
	var ids []string
	for _, proj := range p.projects {
		if proj.selected && !proj.original {
			ids = append(ids, proj.id)
		}
	}
	return ids
}

func (p *projectPicker) toRemove() []string {
	var ids []string
	for _, proj := range p.projects {
		if !proj.selected && proj.original {
			ids = append(ids, proj.id)
		}
	}
	return ids
}

func (p *projectPicker) view(width int) string {
	var header string
	if p.intent == pickerCreate {
		header = fmt.Sprintf("Projects for new item: %s", p.itemTitle)
	} else {
		header = fmt.Sprintf("Projects for: %s", p.itemTitle)
	}

	var lines []string
	lines = append(lines, overlayTitleStyle.Render(header))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("  Select projects (space to toggle, enter to confirm):"))
	lines = append(lines, "")

	for i, proj := range p.projects {
		checkbox := "[ ]"
		if proj.selected {
			checkbox = "[x]"
		}
		line := fmt.Sprintf("%s %s", checkbox, proj.name)
		if i == p.cursor {
			line = pickerSelectedStyle.Render("> " + line)
		} else {
			line = pickerNormalStyle.Render("  " + line)
		}
		lines = append(lines, line)
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(
		fmt.Sprintf("  Currently in %d project(s). Must belong to at least 1.", p.selectedCount()),
	))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("  [Space] Toggle  [Enter] Confirm  [Esc] Cancel"))

	content := strings.Join(lines, "\n")

	return overlayBox(content, width, 40, 60)
}
