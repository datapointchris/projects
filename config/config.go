package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config holds the application configuration loaded from TOML and env vars.
type Config struct {
	Local LocalConfig `mapstructure:"local"`

	// RetiredSync holds a [sync] section, which the command layer warns is ignored.
	// It is declared because ErrorUnused refuses a key the struct has no home for.
	RetiredSync map[string]any `mapstructure:"sync"`

	// ReposRegistry names a registry maintained outside the tool's data directory,
	// which is how a machine sharing one between tools declares it. Empty, or no
	// config file at all, leaves the tool's own data directory as the answer.
	//
	// The field is the config layer alone. $PROJECTS_REPOS_REGISTRY above it and
	// the data directory below it are composed by repos.DefaultPath, so the whole
	// resolution order reads in one place rather than half here and half there.
	ReposRegistry string `mapstructure:"repos_registry"`

	// API, Issuer, ClientID and TokenCommand are goclilogin's keys, and the mode
	// follows its reading of them, so nothing reads these fields. They are declared
	// because ErrorUnused refuses a key the struct has no home for.
	API          string   `mapstructure:"api"`
	Issuer       string   `mapstructure:"issuer"`
	ClientID     string   `mapstructure:"client_id"`
	TokenCommand []string `mapstructure:"token_command"`
}

// LocalConfig holds settings for the local embedded mode.
type LocalConfig struct {
	DBPath string `mapstructure:"db_path"`
}

// FileVariable names the config file, in place of the one under the XDG config
// home. It names the whole file rather than one key, because api, issuer,
// client_id and token_command are one environment, and a repo's .envrc swaps
// them together.
const FileVariable = "PROJECTS_CONFIG"

// FilePath is the config file every reader takes, and the one login writes:
// $PROJECTS_CONFIG where it is set, and otherwise config.toml under the XDG
// config home. Use XDG explicitly rather than Go's UserConfigDir, which returns
// ~/Library/Application Support on macOS, not where CLI tools put config.
func FilePath() string {
	if named := os.Getenv(FileVariable); named != "" {
		return ExpandTilde(named)
	}
	return filepath.Join(userConfigDir(), toolName, "config.toml")
}

// MissingNamedFileError is a $PROJECTS_CONFIG naming a file that does not exist.
// Reading no config there would open the default database, so a write would land
// where the user is not looking.
type MissingNamedFileError struct{ Path string }

func (e *MissingNamedFileError) Error() string {
	return fmt.Sprintf("$%s names %s, which does not exist. Create it, or unset the variable. "+
		"`projects auth login <url>` creates it, holding the API's settings", FileVariable, e.Path)
}

// RefuseMissingNamedFile refuses a $PROJECTS_CONFIG naming a file that does not
// exist. Every reader of the file calls it but login, which creates the file.
func RefuseMissingNamedFile() error {
	if os.Getenv(FileVariable) == "" {
		return nil
	}
	path := FilePath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return &MissingNamedFileError{Path: path}
	}
	return nil
}

// Load reads configuration from the TOML config file and environment variables.
// Priority (highest to lowest): env vars → config file → defaults.
func Load() (*Config, error) {
	if err := RefuseMissingNamedFile(); err != nil {
		return nil, err
	}
	v := newViper()

	v.SetDefault("local.db_path", defaultDBPath())

	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	// Env var overrides
	v.SetEnvPrefix("PROJECTS")
	_ = v.BindEnv("local.db_path", "PROJECTS_DB")

	// ErrorUnused, so a key the tool does not read is refused rather than dropped.
	// viper decodes the merged settings map, and every default set above maps to a
	// field below, so the only keys this can reject are ones the file supplied and
	// the struct has no home for.
	//
	// Without it a misspelled key and an unset one are the same answer. A config
	// naming its registry under a key the tool does not read would fall back to the
	// data directory, find no registry there, and lift the ban with nothing
	// reporting why.
	if err := refuseTokenCommandAsOneString(v); err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// refuseTokenCommandAsOneString refuses a token_command written as one string. It
// is an argument list printing a JSON token response, and a string would otherwise
// fail on a type error naming neither.
func refuseTokenCommandAsOneString(v *viper.Viper) error {
	if command, isString := v.Get("token_command").(string); isString {
		return fmt.Errorf("token_command beside api is a list of arguments, run without a shell, that prints a JSON "+
			"token response: token_command = [\"%s\"]",
			strings.Join(strings.Fields(command), `", "`))
	}
	return nil
}

// newViper points a fresh viper at FilePath. The type is set, because a file
// $PROJECTS_CONFIG names may carry any extension.
func newViper() *viper.Viper {
	v := viper.New()
	v.SetConfigFile(FilePath())
	v.SetConfigType("toml")
	return v
}

// ConfiguredReposRegistry is the repos_registry key with a leading ~ expanded,
// and "" for every way reading it can fail. Absent config is one of those ways
// and is not an error: a machine keeping its registry where the tool expects it
// should not have to hold a file saying so.
//
// A read of its own rather than a field off Load's result, because Load
// refuses a key it does not read. That has nothing to do with where the registry
// lives, so resolving through it would turn an unrelated failure into an empty
// registry, which bans nothing and reports nothing.
func ConfiguredReposRegistry() string {
	v := newViper()
	if err := v.ReadInConfig(); err != nil {
		return ""
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return ""
	}
	return ExpandTilde(cfg.ReposRegistry)
}

// ExpandTilde resolves a leading ~, which a hand-edited config will carry. Left
// literal it names a directory that does not exist, and a registry that is not
// there is not an error here — it reads as an empty registry rather than a bad
// path, so the mistake surfaces as a project-name ban that bans nothing.
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

const (
	toolName      = "projects"
	defaultDBFile = toolName + ".db"
)

func defaultDBPath() string {
	return filepath.Join(DataDir(), defaultDBFile)
}

// userConfigDir returns $XDG_CONFIG_HOME or ~/.config.
func userConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config")
}

// DataDir is the tool's directory under the XDG data home, which every package
// resolving a data path asks for.
func DataDir() string {
	return filepath.Join(userDataDir(), toolName)
}

// userDataDir returns the XDG data directory ($XDG_DATA_HOME or ~/.local/share).
func userDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share")
}
