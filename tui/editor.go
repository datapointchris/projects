package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/todoui/backend"
	"github.com/datapointchris/todoui/model"
)

// editTarget names what an editor session writes back to: an item's notes or a
// project's description, never both.
type editTarget struct {
	itemID    string
	projectID string
}

// externalEditDoneMsg carries the file's contents once the editor exits. err is
// set when the editor could not run or exited non-zero.
type externalEditDoneMsg struct {
	target   editTarget
	original string
	edited   string
	err      error
}

// editorCommand runs $VISUAL, then $EDITOR, then vi. The variable may carry
// arguments, as `code -w` does.
func editorCommand(path string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("EDITOR")
	}
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		fields = []string{"vi"}
	}
	return exec.Command(fields[0], append(fields[1:], path)...)
}

// slugify keeps a file name readable in the editor's buffer line.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 50 {
			break
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func itemNoteFileName(item model.ProjectItem) string {
	if slug := slugify(item.Title); slug != "" {
		return itemHandle(item) + "-" + slug + ".md"
	}
	return itemHandle(item) + ".md"
}

func projectDescriptionFileName(p model.Project) string {
	if slug := slugify(p.Name); slug != "" {
		return slug + ".md"
	}
	return "project-" + shortID(p.ID) + ".md"
}

// openInEditor hands the terminal to the editor on a temporary file holding
// text. The app sits in modeExternalEdit meanwhile, which keeps the background
// pull from rewriting the row the save is about to update.
func (m *App) openInEditor(target editTarget, fileName, text string) tea.Cmd {
	dir, err := os.MkdirTemp("", "todoui-")
	if err != nil {
		return m.flash("Cannot open the editor: " + err.Error())
	}
	path := filepath.Join(dir, fileName)
	body := text
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return m.flash("Cannot open the editor: " + err.Error())
	}

	m.returnMode = m.appMode
	m.appMode = modeExternalEdit
	return tea.ExecProcess(editorCommand(path), func(runErr error) tea.Msg {
		defer func() { _ = os.RemoveAll(dir) }()
		msg := externalEditDoneMsg{target: target, original: text, err: runErr}
		if runErr == nil {
			edited, readErr := os.ReadFile(path)
			msg.edited, msg.err = string(edited), readErr
		}
		return msg
	})
}

// finishExternalEdit saves only a change. Editors end a file with a newline the
// stored text never carried, so trailing newlines are not a change.
func finishExternalEdit(b backend.Backend, msg externalEditDoneMsg) (tea.Cmd, string) {
	if msg.err != nil {
		var exitErr *exec.ExitError
		if errors.As(msg.err, &exitErr) {
			return nil, fmt.Sprintf("Editor exited %d — nothing saved", exitErr.ExitCode())
		}
		return nil, "Editor failed — nothing saved: " + msg.err.Error()
	}
	edited := strings.TrimRight(msg.edited, "\n")
	if edited == strings.TrimRight(msg.original, "\n") {
		return nil, "No changes"
	}
	if msg.target.projectID != "" {
		return updateProjectCmd(b, msg.target.projectID, model.UpdateProject{Description: &edited}), ""
	}
	return updateItemCmd(b, msg.target.itemID, model.UpdateProjectItem{Notes: &edited}), ""
}
