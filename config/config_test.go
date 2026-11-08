package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(FileVariable, "")
	t.Setenv("PROJECTS_DB", "/tmp/test.db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Local.DBPath != "/tmp/test.db" {
		t.Errorf("expected db_path '/tmp/test.db', got %q", cfg.Local.DBPath)
	}
}

func TestDefaultDBPath_XDGOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")

	got := defaultDBPath()
	want := "/custom/data/projects/projects.db"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// repos_registry is a bare top-level key, which TOML requires above the first
// [section] header — declared under one it would belong to that section and
// unmarshal into nothing. The [sync] section below it has to load, so a machine
// whose config still holds one keeps running.
func TestReposRegistryIsReadFromTheConfigFile(t *testing.T) {
	const declared = "/declared/repos.json"
	writeConfig(t, "repos_registry = \""+declared+"\"\n\n[sync]\nenabled = false\n")

	if got := ConfiguredReposRegistry(); got != declared {
		t.Errorf("ConfiguredReposRegistry() = %q, want %q", got, declared)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReposRegistry != declared {
		t.Errorf("cfg.ReposRegistry = %q, want %q", cfg.ReposRegistry, declared)
	}
	if cfg.RetiredSync == nil {
		t.Error("the [sync] section was dropped, so nothing warns that it is ignored")
	}
}

// A repo's .envrc names a dev config while the desk's own holds api. Every reader
// takes the named file, and a name with no file behind it is refused.
func TestProjectsConfigOutranksTheXDGFile(t *testing.T) {
	writeConfig(t, "repos_registry = \"/desk/repos.json\"\napi = \"https://projects.example.com\"\n")
	named := filepath.Join(t.TempDir(), "config.dev.toml")
	if err := os.WriteFile(named, []byte("repos_registry = \"/dev/repos.json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(FileVariable, named)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReposRegistry != "/dev/repos.json" || cfg.API != "" || ConfiguredReposRegistry() != "/dev/repos.json" {
		t.Errorf("read registry %q and api %q, want the named file's", cfg.ReposRegistry, cfg.API)
	}

	t.Setenv(FileVariable, filepath.Join(t.TempDir(), "absent.toml"))
	var missing *MissingNamedFileError
	if _, err := Load(); !errors.As(err, &missing) {
		t.Errorf("Load with a missing named file answered %v, want MissingNamedFileError", err)
	}
}

// A machine keeping its registry where the tool expects it holds no config at
// all, so an absent or unreadable file answers "" rather than failing.
func TestConfiguredReposRegistryIsEmptyWithoutAConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(FileVariable, "")
	if got := ConfiguredReposRegistry(); got != "" {
		t.Errorf("ConfiguredReposRegistry() = %q, want empty", got)
	}

	writeConfig(t, "repos_registry = [not toml\n")
	if got := ConfiguredReposRegistry(); got != "" {
		t.Errorf("a malformed config must answer empty, got %q", got)
	}
}

// writeConfig points $XDG_CONFIG_HOME at a directory holding body as the tool's
// config file.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
		t.Fatalf("creating config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects", "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(FileVariable, "")
}
