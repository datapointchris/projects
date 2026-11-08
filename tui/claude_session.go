package tui

import (
	"fmt"
	"os"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
)

// The first message of the Claude session C opens begins with these. They name the record by its
// handle alone, so the session reads the record's words as data with the command they name.
const (
	itemSessionBrief    = "This is projects item %[1]s, opened from `projects tui`. `projects items show %[1]s` prints it whole."
	projectSessionBrief = "This is the projects project %[1]s, opened from `projects tui`. `projects show %[1]s` prints it with its open items."
)

func (m *App) claudeOnItem() tea.Cmd {
	item := m.currentItem()
	repo := ""
	if item.Repo != nil {
		repo = *item.Repo
	}
	handle := itemHandle(item.ProjectItem)
	return m.openClaudeSession(handle, repo, fmt.Sprintf(itemSessionBrief, handle))
}

// claudeOnProject opens in the checkout of the one repo the project's items name, if they name one.
func (m *App) claudeOnProject() tea.Cmd {
	p := m.currentProject()
	repo := ""
	if len(p.Repos) == 1 {
		repo = p.Repos[0]
	}
	return m.openClaudeSession(p.Name, repo, fmt.Sprintf(projectSessionBrief, p.ID))
}

// openClaudeSession is C: a Claude session in the repo's checkout, which gotermboard opens.
func (m *App) openClaudeSession(handle, repo, brief string) tea.Cmd {
	directory, fallback, err := m.sessionDirectory(handle, repo)
	if err != nil {
		return m.flash(err.Error())
	}
	return gotermboard.OpenClaudeSession(gotermboard.ClaudeSession{On: handle, Dir: directory, Fallback: fallback, Brief: brief})
}

// tookClaudeOutcome flashes what came of C. A session that held the terminal may have changed
// what the board shows, so the board reads again.
func (m *App) tookClaudeOutcome(outcome gotermboard.ClaudeSessionOutcome) tea.Cmd {
	said := []rune(outcome.Said)
	if len(said) > 0 {
		said[0] = unicode.ToUpper(said[0])
	}
	flash := m.flash(string(said))
	if outcome.HeldTerminal {
		return tea.Batch(flash, m.rereadCmd())
	}
	return flash
}

// sessionDirectory is where C opens Claude: the checkout the registry names for the repo, else
// the board's own directory with why it fell back. A checkout named and missing opens nothing,
// so a session never plans against another repo's code.
func (m *App) sessionDirectory(handle, repo string) (directory, fallback string, err error) {
	if repo != "" && m.where.Checkout != nil {
		checkout, named, err := m.where.Checkout(repo)
		if err != nil {
			return "", "", err
		}
		if named {
			if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
				return "", "", fmt.Errorf("%s is not checked out at %s, so no session opened on %s", repo, checkout, handle)
			}
			return checkout, "", nil
		}
	}
	here, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("no directory to open a session on %s in: %w", handle, err)
	}
	if repo == "" {
		return here, fmt.Sprintf("%s names no one repo", handle), nil
	}
	return here, fmt.Sprintf("the repo registry names no checkout of %s", repo), nil
}
