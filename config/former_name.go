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

// MIGRATION: the tool was called todoui. A machine that upgrades still holds data, a config
// and variables under that name, and to this tool they read as if never written. Delete this
// file, its calls in cli/data_source.go, former_name_test.go, and the passages on it in README
// and CLAUDE.md once no machine holds a todoui data or config directory and no checkout's .envrc
// exports a TODOUI_* variable.
const formerName = "todoui"

// Ignoring TODOUI_DB would open the default database where it chose another, and the others
// are refused so a half-renamed environment never runs.
func init() {
	for _, renamed := range []struct{ retired, replacement string }{
		{"TODOUI_DB", "PROJECTS_DB"},
		{"TODOUI_REPOS_REGISTRY", "PROJECTS_REPOS_REGISTRY"},
		{"TODOUI_NO_AUTO_UPDATE", "PROJECTS_NO_AUTO_UPDATE"},
	} {
		retiredVariables = append(retiredVariables, retiredVariable{
			name:   renamed.retired,
			remedy: fmt.Sprintf("and projects reads $%s. Move the value there, or unset it.", renamed.replacement),
		})
	}
}

// RefuseConfigUnderFormerName stops a run while the config sits only at the former path.
// Reading none would drop its api and send every command to a database on this machine.
func RefuseConfigUnderFormerName() error {
	former := filepath.Join(userConfigDir(), formerName, "config.toml")
	current := filepath.Join(userConfigDir(), toolName, "config.toml")
	if !pathExists(former) || pathExists(current) {
		return nil
	}
	return fmt.Errorf("%s is still under the former name, and projects reads %s. Move it there, or relink it, then run projects again", former, current)
}

// MoveDataUnderFormerName moves the former data directory's contents across, and must run
// before anything opens dbPath. A dbPath other than the default moves nothing, so a dev run
// cannot carry the real data with it.
func MoveDataUnderFormerName(dbPath string) error {
	former := filepath.Join(userDataDir(), formerName)
	if strings.HasPrefix(dbPath, former+string(filepath.Separator)) {
		return fmt.Errorf("the database path %s is inside %s, which the first default run moves. Name %s instead",
			dbPath, former, filepath.Join(DataDir(), filepath.Base(dbPath)))
	}
	if dbPath != defaultDBPath() {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(autoupdate.StateHome(), formerName))

	entries, err := os.ReadDir(former)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", former, err)
	}

	current := DataDir()
	for _, entry := range entries {
		moved := movedName(entry.Name())
		if moved != entry.Name() && pathExists(filepath.Join(current, moved)) {
			return fmt.Errorf("%s and %s both exist, so nothing moved. A todoui binary run after the move recreates the first empty, and the second then holds the moved data. Remove that binary, and delete the first file if it holds nothing you need",
				filepath.Join(former, entry.Name()), filepath.Join(current, moved))
		}
	}
	if err := os.MkdirAll(current, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", current, err)
	}
	// Backward, so each database's -wal arrives before it: rows only the WAL holds are committed.
	for _, entry := range slices.Backward(entries) {
		source := filepath.Join(former, entry.Name())
		destination := filepath.Join(current, movedName(entry.Name()))
		// A dev.db already on the new side stays behind, because it must not stop the real database.
		if pathExists(destination) {
			continue
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("moving %s: %w", source, err)
		}
	}
	// Fails while a file stayed behind, and nothing reads the directory either way.
	_ = os.Remove(former)
	return nil
}

// movedName is a former database file's name under the current name, and any other entry's own.
func movedName(name string) string {
	if suffix, ok := strings.CutPrefix(name, formerName+".db"); ok {
		return defaultDBFile + suffix
	}
	return name
}

// pathExists uses Lstat, so a link whose target is gone still counts as there.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
