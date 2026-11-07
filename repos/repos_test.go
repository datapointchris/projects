package repos

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRegistry(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	return path
}

// A machine without the registry still has to be able to create projects.
func TestMissingRegistryBansNothing(t *testing.T) {
	registry, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing registry must not be an error, got %v", err)
	}
	if registry.Knows("projects") {
		t.Error("a missing registry must not ban anything")
	}
}

func TestMalformedRegistryIsAnError(t *testing.T) {
	if _, err := Load(writeRegistry(t, "{not json")); err == nil {
		t.Error("a corrupt registry must surface rather than silently lifting the ban")
	}
}

// withConfig points $XDG_CONFIG_HOME at a directory holding body as the tool's
// config, or at an empty one when body is "". Every DefaultPath test needs it:
// the machine running the test maintains a real config, and without the
// redirect the middle rung answers from that instead of from the case at hand.
func withConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
			t.Fatalf("creating config directory: %v", err)
		}
		path := filepath.Join(dir, "projects", "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
}

// The compiled default names nothing outside the tool's own XDG data directory,
// which is what keeps a generic tool generic.
func TestDefaultPathIsTheToolsOwnDataDirectory(t *testing.T) {
	t.Setenv("PROJECTS_REPOS_REGISTRY", "")
	withConfig(t, "")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/tmp/xdg/projects/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

// The three rungs in order: the tool's own variable, then its own config key,
// then its own data directory. A machine that maintains its registry elsewhere
// says so in the config.
func TestDefaultPathResolvesInThreeRungs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	withConfig(t, "repos_registry = \"/declared/repos.json\"\n")

	t.Setenv("PROJECTS_REPOS_REGISTRY", "")
	if got, want := DefaultPath(), "/declared/repos.json"; got != want {
		t.Errorf("the config key must beat the data directory: got %q, want %q", got, want)
	}

	t.Setenv("PROJECTS_REPOS_REGISTRY", "/from/env.json")
	if got, want := DefaultPath(), "/from/env.json"; got != want {
		t.Errorf("the variable must beat the config key: got %q, want %q", got, want)
	}
}

// A hand-edited config carries a ~, and left literal it names a directory that
// does not exist — which reads here as an empty registry that bans nothing
// rather than as a bad path.
func TestTheConfiguredPathExpandsALeadingTilde(t *testing.T) {
	t.Setenv("PROJECTS_REPOS_REGISTRY", "")
	withConfig(t, "repos_registry = \"~/registry/repos.json\"\n")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if got, want := DefaultPath(), filepath.Join(home, "registry", "repos.json"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestKnowsIdentifiesARepoName(t *testing.T) {
	path := writeRegistry(t, `{"repos":[{"name":"projects"},{"name":"dotfiles"}]}`)
	registry, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, name := range []string{"projects", "Projects", "  dotfiles  ", "DOTFILES"} {
		if !registry.Knows(name) {
			t.Errorf("Knows(%q) = false; a ban a capital letter walks around is decoration", name)
		}
	}
	for _, name := range []string{"", "projects sync improvements", "Extract xx from dotfiles"} {
		if registry.Knows(name) {
			t.Errorf("Knows(%q) = true; bounded work named after a repo is still bounded work", name)
		}
	}
}
