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
	"slices"
	"strings"

	"github.com/datapointchris/projects/config"
)

type registryFile struct {
	Repos []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"repos"`
}

// Registry is the set of known repo names, empty when the file is absent, and where each is
// checked out.
type Registry struct {
	names     map[string]bool
	checkouts map[string]string
	path      string
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
	return filepath.Join(config.DataDir(), "repos.json")
}

// Load reads the registry at path. A missing file is not an error: it yields a
// registry that knows no names.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Registry{path: path}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading repo registry %s: %w", path, err)
	}
	var parsed registryFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing repo registry %s: %w", path, err)
	}
	names := make(map[string]bool, len(parsed.Repos))
	checkouts := make(map[string]string, len(parsed.Repos))
	for _, repo := range parsed.Repos {
		names[repo.Name] = true
		if repo.Path != "" {
			checkouts[repo.Name] = config.ExpandTilde(repo.Path)
		}
	}
	return &Registry{names: names, checkouts: checkouts, path: path}, nil
}

// Checkout is where the registry says name is checked out, and whether it names a place.
func (r *Registry) Checkout(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	checkout, named := r.checkouts[name]
	return checkout, named
}

// Available reports whether the registry was found and had any repos in it.
func (r *Registry) Available() bool {
	return r != nil && len(r.names) > 0
}

// Names is every registered repo, sorted, for a prompt to offer.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.names))
	for name := range r.names {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Validate refuses a repo tag the registry does not hold, naming the near ones.
// An empty name is the untagged work, and a missing registry refuses nothing.
//
// On a write a mistyped tag drops the item out of every repo view, and on a read
// it selects nothing, which reads the same as a repo with no work.
func (r *Registry) Validate(name string) error {
	if name == "" || !r.Available() || r.names[name] {
		return nil
	}
	lowered := strings.ToLower(name)
	var near []string
	for _, known := range r.Names() {
		lowerKnown := strings.ToLower(known)
		if strings.Contains(lowerKnown, lowered) || strings.HasPrefix(lowered, lowerKnown) {
			near = append(near, known)
		}
	}
	if len(near) > 0 {
		return fmt.Errorf("unknown repo %q — did you mean %s?", name, strings.Join(near[:min(len(near), 3)], ", "))
	}
	return fmt.Errorf("unknown repo %q — %s lists the registered names", name, r.path)
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
