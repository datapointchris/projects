package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// No config file, no env vars — should use defaults
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PROJECTS_SYNC", "")
	t.Setenv("PROJECTS_SYNC_URL", "")
	t.Setenv("PROJECTS_DB", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Sync.Enabled {
		t.Error("expected sync disabled by default")
	}
	if cfg.Local.DBPath == "" {
		t.Error("expected non-empty default db_path")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("PROJECTS_DB", "/tmp/test.db")
	t.Setenv("PROJECTS_SYNC", "true")
	t.Setenv("PROJECTS_SYNC_URL", "https://example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Local.DBPath != "/tmp/test.db" {
		t.Errorf("expected db_path '/tmp/test.db', got %q", cfg.Local.DBPath)
	}
	if !cfg.Sync.Enabled {
		t.Error("expected sync enabled via env")
	}
	if cfg.Sync.APIURL != "https://example.com" {
		t.Errorf("expected sync url 'https://example.com', got %q", cfg.Sync.APIURL)
	}
}

func TestLoad_SyncEnabledWithoutURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PROJECTS_SYNC", "true")
	t.Setenv("PROJECTS_SYNC_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when sync enabled without api_url")
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

// The interval is the TUI's background pull period, so a missing default would
// mean no automatic sync at all.
func TestLoad_SyncIntervalDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PROJECTS_SYNC_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync.Interval != 2*time.Minute {
		t.Errorf("expected a 2m default interval, got %v", cfg.Sync.Interval)
	}
}

func TestLoad_SyncIntervalFromEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PROJECTS_SYNC_INTERVAL", "45s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync.Interval != 45*time.Second {
		t.Errorf("expected 45s from the environment, got %v", cfg.Sync.Interval)
	}
}

// repos_registry is a bare top-level key, which TOML requires above the first
// [section] header — declared under one it would belong to that section and
// unmarshal into nothing.
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
}

// A machine keeping its registry where the tool expects it holds no config at
// all, so an absent or unreadable file answers "" rather than failing.
func TestConfiguredReposRegistryIsEmptyWithoutAConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
}

const syncedConfig = "[sync]\nenabled = true\napi_url = \"https://api.example\"\ntoken_command = \"print-token --for api\"\n"

func TestTheTokenCommandIsReadFromTheConfigFile(t *testing.T) {
	writeConfig(t, syncedConfig)
	t.Setenv("PROJECTS_SYNC_URL", "")
	t.Setenv("PROJECTS_SYNC_TOKEN_COMMAND", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync.TokenCommand != "print-token --for api" {
		t.Errorf("token command %q, want the file's", cfg.Sync.TokenCommand)
	}
}

// A dev .envrc points the URL at another API. The file's command was written for
// the file's URL, and must not hand its token to that one.
func TestAURLFromTheEnvironmentTakesNoTokenCommandFromTheFile(t *testing.T) {
	writeConfig(t, syncedConfig)
	t.Setenv("PROJECTS_SYNC_URL", "https://dev.example")
	t.Setenv("PROJECTS_SYNC_TOKEN_COMMAND", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync.TokenCommand != "" {
		t.Errorf("token command %q paired with the environment's URL, want none", cfg.Sync.TokenCommand)
	}

	t.Setenv("PROJECTS_SYNC_TOKEN_COMMAND", "print-token --for dev")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync.TokenCommand != "print-token --for dev" {
		t.Errorf("token command %q, want the environment's", cfg.Sync.TokenCommand)
	}
}

// Split on spaces, a blank command is nothing to run, and silently sending no
// token would read as the API refusing a good one.
func TestABlankTokenCommandIsRefused(t *testing.T) {
	writeConfig(t, "[sync]\nenabled = true\napi_url = \"https://api.example\"\ntoken_command = \"   \"\n")
	t.Setenv("PROJECTS_SYNC_URL", "")
	t.Setenv("PROJECTS_SYNC_TOKEN_COMMAND", "")

	if _, err := Load(); err == nil {
		t.Fatal("a blank token_command loaded")
	}
}

// time.NewTicker panics on a non-positive duration, so a typo here would take
// the TUI down on launch rather than degrading to a slower sync.
func TestLoad_SyncIntervalIsFloored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, given := range []string{"0s", "-5m", "1s"} {
		t.Setenv("PROJECTS_SYNC_INTERVAL", given)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(%s): %v", given, err)
		}
		if cfg.Sync.Interval < minSyncInterval {
			t.Errorf("interval %q left below the floor: %v", given, cfg.Sync.Interval)
		}
	}
}
