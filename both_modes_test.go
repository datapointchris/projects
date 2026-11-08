package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/datapointchris/tuidrive"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/datapointchris/projects/tui"
)

// Each mode runs the built binary over the same seeded data: a database on this
// machine, served through the API's routes inside the process, and the API's
// routes over HTTP. Each --json read must match its schema, and both modes must
// answer the same.
func TestTheCommandsAnswerTheSameInBothModes(t *testing.T) {
	binary := tuidrive.Build(t, ".")
	titles := map[string][]string{}
	for _, mode := range []string{"database", "api"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode == "api" {
				served := filepath.Join(t.TempDir(), "served.db")
				seedFixtureDatabase(t, served)
				writeAPIConfig(t, dir, serveOverHTTP(t, served))
			} else {
				seedFixtureDatabase(t, filepath.Join(dir, "projects.db"))
			}
			run := commandRunner(t, binary, dir)

			created := run(0, "items", "create", "--project", "Replace the kitchen faucet", "--title", "Buy plumber's tape", "--json")
			var item struct{ Number *int }
			if err := json.Unmarshal(created, &item); err != nil || item.Number == nil {
				t.Fatalf("create answered %s, want a numbered item: %v", created, err)
			}
			number := strconv.Itoa(*item.Number)

			for _, read := range []struct {
				schema string
				args   []string
			}{
				{"project-with-items", []string{"show", "Replace the kitchen faucet", "--json"}},
				{"item-with-tasks-and-blockers", []string{"items", "show", number, "--json"}},
				{"focus-queue", []string{"next", "--json"}},
				{"dependency-tree", []string{"items", "dependencies", "tree", "--json"}},
			} {
				matchesSchema(t, read.schema, run(0, read.args...))
			}
			for _, view := range tui.ViewNames() {
				matchesSchema(t, "board-snapshot", run(0, "tui", "--json", "--view", view))
			}
			if mode == "api" {
				matchesSchema(t, "auth-status", run(0, "auth", "status", "--json"))
			}

			var shown struct{ Items []struct{ Title string } }
			if err := json.Unmarshal(run(0, "show", "Replace the kitchen faucet", "--json"), &shown); err != nil {
				t.Fatal(err)
			}
			for _, it := range shown.Items {
				titles[mode] = append(titles[mode], it.Title)
			}
			if !slices.Contains(titles[mode], "Buy plumber's tape") {
				t.Errorf("show listed %q, want the created item", titles[mode])
			}

			run(2, "items", "create", "--title", "No project given", "--no-input")
			run(1, "show", "A project nobody made")
			if _, err := os.Stat(filepath.Join(dir, "projects.db")); mode == "api" && err == nil {
				t.Error("API mode opened a database")
			}
		})
	}
	if !slices.Equal(titles["database"], titles["api"]) {
		t.Errorf("the database listed %q and the API %q", titles["database"], titles["api"])
	}

	t.Run("an API that never answers exits 4", func(t *testing.T) {
		dir := t.TempDir()
		writeAPIConfig(t, dir, "http://127.0.0.1:1")
		commandRunner(t, binary, dir)(4, "list")
	})
}

// commandRunner runs the binary in dir with its config under dir, and fails the
// test when a run exits other than want.
func commandRunner(t *testing.T, binary, dir string) func(want int, args ...string) []byte {
	return func(want int, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + dir,
			"PROJECTS_DB=projects.db",
			"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Fatalf("projects %q exited %d, want %d\nstdout: %s\nstderr: %s", args, code, want, stdout.String(), stderr.String())
		}
		return stdout.Bytes()
	}
}

// matchesSchema checks answer against cli/schemas/<name>.schema.json. Every schema there is
// loaded under its $id, so one can refer to another by it.
func matchesSchema(t *testing.T, name string, answer []byte) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("cli", "schemas", "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiled := ""
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		id, _ := document.(map[string]any)["$id"].(string)
		if err := compiler.AddResource(id, document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if filepath.Base(path) == name+".schema.json" {
			compiled = id
		}
	}
	schema, err := compiler.Compile(compiled)
	if err != nil {
		t.Fatalf("compiling %s: %v", name, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(answer))
	if err != nil {
		t.Fatalf("%s answered %s, which is not JSON: %v", name, answer, err)
	}
	if err := schema.Validate(value); err != nil {
		t.Errorf("%s does not match its schema: %v\n%s", name, err, answer)
	}
}
