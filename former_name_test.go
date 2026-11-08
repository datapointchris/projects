package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/tuidrive"

	"github.com/datapointchris/projects/cli"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite"
)

// Each case runs the built binary under a scratch home, because the exit code and the
// order against opening the database belong to main. `list` reaches the move and opens
// the database, and needs no terminal.
func TestFormerNameOnFirstRun(t *testing.T) {
	binary := tuidrive.Build(t, ".")

	t.Run("the database moves with the rows only its WAL holds", func(t *testing.T) {
		home := newScratchHome(t)
		former, current := home.dataDir("todoui"), home.dataDir("projects")
		seedFormerDatabaseWithUnflushedWAL(t, former)
		writeFile(t, filepath.Join(former, "dev.db"), "dev")

		if output, code := home.run(t, binary); code != 0 {
			t.Fatalf("exit %d, output %q, want the list to answer", code, output)
		}

		if _, err := os.Lstat(former); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", former, err)
		}
		if got := readFile(t, filepath.Join(current, "dev.db")); got != "dev" {
			t.Errorf("dev.db read %q after the move", got)
		}
		database, err := sqlite.Open(filepath.Join(current, "projects.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = database.Close() }()
		projects, err := cli.LocalService(database, "").ListProjectsByStatus(model.StatusAll)
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || projects[0].Name != "Held only in the WAL" {
			t.Errorf("projects after the move: %+v", projects)
		}
	})

	t.Run("both directories holding a database refuses and moves nothing", func(t *testing.T) {
		home := newScratchHome(t)
		formerDB := filepath.Join(home.dataDir("todoui"), "todoui.db")
		currentDB := filepath.Join(home.dataDir("projects"), "projects.db")
		writeFile(t, formerDB, "former")
		writeFile(t, currentDB, "current")

		output, code := home.run(t, binary)

		if code != 1 || !strings.Contains(output, formerDB) || !strings.Contains(output, currentDB) {
			t.Errorf("exit %d, output %q, want exit 1 naming both", code, output)
		}
		if readFile(t, formerDB) != "former" || readFile(t, currentDB) != "current" {
			t.Error("a refused move changed a database")
		}
	})

	t.Run("a named database leaves the former directory alone", func(t *testing.T) {
		home := newScratchHome(t)
		formerDB := filepath.Join(home.dataDir("todoui"), "todoui.db")
		writeFile(t, formerDB, "former")

		home.run(t, binary, "PROJECTS_DB="+filepath.Join(home.root, "named.db"))

		if readFile(t, formerDB) != "former" {
			t.Error("a run naming its database touched the former one")
		}
	})

	t.Run("a config left at the former path refuses before it is silently ignored", func(t *testing.T) {
		home := newScratchHome(t)
		formerConfig := filepath.Join(home.root, "config", "todoui", "config.toml")
		formerDB := filepath.Join(home.dataDir("todoui"), "todoui.db")
		writeFile(t, formerConfig, "api = \"https://projects.example\"\n")
		writeFile(t, formerDB, "former")

		output, code := home.run(t, binary)

		if code != 1 || !strings.Contains(output, formerConfig) {
			t.Errorf("exit %d, output %q, want exit 1 naming %s", code, output, formerConfig)
		}
		if readFile(t, formerDB) != "former" {
			t.Error("a refused run moved the former database")
		}
	})

	t.Run("a .envrc edited before the first default run leaves dev.db behind and moves the real database", func(t *testing.T) {
		home := newScratchHome(t)
		former, current := home.dataDir("todoui"), home.dataDir("projects")
		writeFile(t, filepath.Join(former, "todoui.db"), "real")
		writeFile(t, filepath.Join(former, "dev.db"), "old dev")
		writeFile(t, filepath.Join(current, "dev.db"), "new dev")

		home.run(t, binary)

		if readFile(t, filepath.Join(former, "dev.db")) != "old dev" || readFile(t, filepath.Join(current, "dev.db")) != "new dev" {
			t.Error("a dev.db on both sides was overwritten")
		}
		if _, err := os.Lstat(filepath.Join(former, "todoui.db")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the real database stayed behind: %v", err)
		}
	})

	t.Run("a TODOUI_DB value moved across unchanged refuses, naming the new directory", func(t *testing.T) {
		home := newScratchHome(t)
		named := filepath.Join(home.dataDir("todoui"), "dev.db")

		output, code := home.run(t, binary, "PROJECTS_DB="+named)

		if code != 1 || !strings.Contains(output, filepath.Join(home.dataDir("projects"), "dev.db")) {
			t.Errorf("exit %d, output %q, want exit 1 naming the new path", code, output)
		}
		if _, err := os.Lstat(named); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a refused run created %s: %v", named, err)
		}
	})

	t.Run("a retired variable refuses before anything is touched", func(t *testing.T) {
		home := newScratchHome(t)
		formerDB := filepath.Join(home.dataDir("todoui"), "todoui.db")
		writeFile(t, formerDB, "former")

		output, code := home.run(t, binary, "TODOUI_DB="+filepath.Join(home.root, "named.db"))

		if code != 2 || !strings.Contains(output, "$PROJECTS_DB") {
			t.Errorf("exit %d, output %q, want exit 2 naming the new variable", code, output)
		}
		if readFile(t, formerDB) != "former" {
			t.Error("a refused run touched the former database")
		}
	})
}

type scratchHome struct{ root string }

func newScratchHome(t *testing.T) scratchHome {
	t.Helper()
	return scratchHome{root: t.TempDir()}
}

func (h scratchHome) dataDir(name string) string {
	return filepath.Join(h.root, "data", name)
}

func (h scratchHome) run(t *testing.T, binary string, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, "list")
	cmd.Dir = h.root
	cmd.Env = append([]string{
		"HOME=" + h.root,
		"XDG_DATA_HOME=" + filepath.Join(h.root, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(h.root, "config"),
		"XDG_STATE_HOME=" + filepath.Join(h.root, "state"),
	}, env...)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(output), cmd.ProcessState.ExitCode()
}

// seedFormerDatabaseWithUnflushedWAL copies a database while its connection is open,
// because closing the last connection checkpoints the WAL and deletes it.
func seedFormerDatabaseWithUnflushedWAL(t *testing.T, dir string) {
	t.Helper()
	live := filepath.Join(t.TempDir(), "live.db")
	database, err := sqlite.Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, err := cli.LocalService(database, "").CreateProject(model.CreateProject{Name: "Held only in the WAL"}); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		copyFile(t, live+suffix, filepath.Join(dir, "todoui.db"+suffix))
	}
	if info, err := os.Stat(filepath.Join(dir, "todoui.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("the copied WAL is empty: %v", err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	from, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = from.Close() }()
	to, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = to.Close() }()
	if _, err := io.Copy(to, from); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
