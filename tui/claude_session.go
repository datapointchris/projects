package tui

import (
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/projects/repos"
)

// The first message of the Claude session C opens. It names the record by its handle alone, so
// the session reads the record's words as data with the command it names.
const (
	itemSessionBrief = "This is projects item %[1]s, opened from `projects tui`. " +
		"`projects items show %[1]s` prints it whole. Read it, say what it asks for and what you would do, " +
		"then wait before changing anything."
	projectSessionBrief = "This is the projects project %[1]s, opened from `projects tui`. " +
		"`projects show %[1]s` prints it with its open items. Read it, say what it asks for and what you would do, " +
		"then wait before changing anything."
)

// claudeSessionEndedMsg is a Claude session that held the terminal giving it back.
type claudeSessionEndedMsg struct {
	handle string
	err    error
}

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

// openClaudeSession is C: a Claude session in the repo's checkout. Inside tmux it opens in a
// window of its own, so the board stays usable while the session works, and outside tmux it
// takes the board's terminal until it ends.
func (m *App) openClaudeSession(handle, repo, brief string) tea.Cmd {
	if _, err := exec.LookPath("claude"); err != nil {
		return m.flash(fmt.Sprintf("claude is not on PATH, so no session opened on %s", handle))
	}
	directory, fallback, err := sessionDirectory(handle, repo)
	if err != nil {
		return m.flash(err.Error())
	}
	if os.Getenv("TMUX") != "" {
		if err := gotermboard.OpenWindow("claude "+handle, directory, []string{"claude", brief}); err != nil {
			return m.flash(err.Error())
		}
		return m.flash(fmt.Sprintf("Opened a Claude session on %s in a tmux window, in %s%s", handle, directory, fallback))
	}
	session := exec.Command("claude", brief)
	session.Dir = directory
	return tea.ExecProcess(session, func(err error) tea.Msg { return claudeSessionEndedMsg{handle: handle, err: err} })
}

// sessionDirectory is where C opens Claude: the checkout the registry names for the repo, else
// the board's own directory with why it fell back. A checkout named and missing opens nothing,
// so a session never plans against another repo's code.
func sessionDirectory(handle, repo string) (directory, fallback string, err error) {
	if repo != "" {
		registry, err := repos.Load(repos.DefaultPath())
		if err != nil {
			return "", "", err
		}
		if checkout, named := registry.Checkout(repo); named {
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
		return here, fmt.Sprintf(", since %s names no one repo", handle), nil
	}
	return here, fmt.Sprintf(", since the repo registry names no checkout of %s", repo), nil
}
