package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

// MIGRATION (v1.21.0): the tool was called todoui. A machine that upgrades still holds
// data and variables under that name, and to this tool they read as if never written.
// Delete this file, its two calls in main.go, former_name_test.go and the README's
// section on moving once no machine holds a todoui data directory.
const (
	formerName  = "todoui"
	currentName = "projects"
)

var retiredVariables = []struct{ retired, replacement string }{
	{"TODOUI_DB", "PROJECTS_DB"},
	{"TODOUI_SYNC", "PROJECTS_SYNC"},
	{"TODOUI_SYNC_URL", "PROJECTS_SYNC_URL"},
	{"TODOUI_SYNC_TOKEN_COMMAND", "PROJECTS_SYNC_TOKEN_COMMAND"},
	{"TODOUI_SYNC_INTERVAL", "PROJECTS_SYNC_INTERVAL"},
	{"TODOUI_REPOS_REGISTRY", "PROJECTS_REPOS_REGISTRY"},
	{"TODOUI_NO_AUTO_UPDATE", "PROJECTS_NO_AUTO_UPDATE"},
}

// RetiredVariableRefusal names the first retired variable still set, or "" where none is.
// Ignoring one would open the default database where the variable chose another.
func RetiredVariableRefusal() string {
	for _, variable := range retiredVariables {
		if os.Getenv(variable.retired) != "" {
			return fmt.Sprintf("$%s is set, and projects reads $%s. Move the value there, or unset it.",
				variable.retired, variable.replacement)
		}
	}
	return ""
}

// MoveDataUnderFormerName moves the former data directory under the current name, and
// must run before anything opens dbPath. A dbPath other than the default moves nothing.
func MoveDataUnderFormerName(dbPath string) error {
	if dbPath != defaultDBPath() {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(autoupdate.StateHome(), formerName))

	former := filepath.Join(userDataDir(), formerName)
	entries, err := os.ReadDir(former)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", former, err)
	}

	current := filepath.Dir(dbPath)
	for _, entry := range entries {
		source := filepath.Join(former, entry.Name())
		destination := filepath.Join(current, movedName(entry.Name()))
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("%s and %s both exist, so nothing moved. Move one of them aside, then run projects again", source, destination)
		}
	}
	if err := os.MkdirAll(current, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", current, err)
	}
	// Backward, so each database's -wal arrives before it: rows only the WAL holds are committed.
	for _, entry := range slices.Backward(entries) {
		source := filepath.Join(former, entry.Name())
		if err := os.Rename(source, filepath.Join(current, movedName(entry.Name()))); err != nil {
			return fmt.Errorf("moving %s: %w", source, err)
		}
	}
	if err := os.Remove(former); err != nil {
		return fmt.Errorf("removing %s: %w", former, err)
	}
	return nil
}

// movedName is a former database file's name under the current name, and any other entry's own.
func movedName(name string) string {
	if suffix, ok := strings.CutPrefix(name, formerName+".db"); ok {
		return currentName + ".db" + suffix
	}
	return name
}
