// Package repos reads the repo registry, whose names a project may not take.
//
// The registry is optional. Where it does not exist nothing is banned, because
// refusing to create a project on a machine without one is worse than a
// project named after a repo.
package repos

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/projects/config"
)

type registryFile struct {
	Repos []struct {
		Name string `json:"name"`
	} `json:"repos"`
}

// Registry is the set of known repo names, empty when the file is absent.
type Registry struct {
	names map[string]bool
}

// DefaultPath is where this machine keeps the registry: $PROJECTS_REPOS_REGISTRY,
// then repos_registry in the tool's config, then the tool's own data directory per
// the XDG split.
//
// The last rung is the compiled default and names nothing outside this tool's
// own directories, which is what keeps a generic tool generic. The two above it
// are how a machine says the file is maintained elsewhere.
//
// A registry kept elsewhere is declared in the config file, because the two
// other ways to point at it fail silently:
//
//   - A symlink from the data directory is declared nowhere and reported by
//     nothing. Once a link is clobbered the registry forks, and both copies
//     still parse.
//   - A variable exported from a shell profile never reaches a process that
//     sources no profile, such as a systemd timer. The rung would be empty in
//     exactly the unattended runs it exists to serve.
//
// The config file reaches every process. The one variable read carries
// the tool's own prefix, so no other tool's vocabulary is compiled into it.
func DefaultPath() string {
	if p := os.Getenv("PROJECTS_REPOS_REGISTRY"); p != "" {
		return config.ExpandTilde(p)
	}
	if declared := config.ConfiguredReposRegistry(); declared != "" {
		return declared
	}
	return filepath.Join(config.UserDataDir(), "projects", "repos.json")
}

// Load reads the registry at path. A missing file is not an error: it yields a
// registry that knows no names.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading repo registry %s: %w", path, err)
	}
	var parsed registryFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing repo registry %s: %w", path, err)
	}
	names := make(map[string]bool, len(parsed.Repos))
	for _, repo := range parsed.Repos {
		names[repo.Name] = true
	}
	return &Registry{names: names}, nil
}

// Available reports whether the registry was found and had any repos in it.
func (r *Registry) Available() bool {
	return r != nil && len(r.names) > 0
}

// Knows reports whether name is a registered repo. Case- and
// space-insensitive, because "Projects " names the same thing "projects" does and a
// ban a capital letter walks around is decoration.
func (r *Registry) Knows(name string) bool {
	if !r.Available() {
		return false
	}
	wanted := strings.ToLower(strings.TrimSpace(name))
	if wanted == "" {
		return false
	}
	for known := range r.names {
		if strings.ToLower(known) == wanted {
			return true
		}
	}
	return false
}
