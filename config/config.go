package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config holds the application configuration loaded from TOML and env vars.
type Config struct {
	Local LocalConfig `mapstructure:"local"`
	Sync  SyncConfig  `mapstructure:"sync"`

	// ReposRegistry names a registry maintained outside the tool's data directory,
	// which is how a machine sharing one between tools declares it. Empty, or no
	// config file at all, leaves the tool's own data directory as the answer.
	//
	// The field is the config layer alone. $PROJECTS_REPOS_REGISTRY above it and
	// the data directory below it are composed by repos.DefaultPath, so the whole
	// resolution order reads in one place rather than half here and half there.
	ReposRegistry string `mapstructure:"repos_registry"`
}

// SyncConfig holds settings for background sync with the remote API.
type SyncConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`

	// TokenCommand prints a bearer token for APIURL, the way a CLI's
	// `auth token` does. It is split on spaces and run without a shell. Empty
	// sends no Authorization header, which a dev API that sets its own identity
	// wants.
	TokenCommand string `mapstructure:"token_command"`

	// Interval is the longest the tool will go without reconciling: the TUI's
	// background pull period.
	Interval time.Duration `mapstructure:"interval"`
}

// LocalConfig holds settings for the local embedded mode.
type LocalConfig struct {
	DBPath string `mapstructure:"db_path"`
}

// Load reads configuration from the TOML config file and environment variables.
// Priority (highest to lowest): env vars → config file → defaults.
func Load() (*Config, error) {
	v := newViper()

	// Defaults. Every pull is a full reconcile, so the interval trades API load
	// against staleness. Two minutes keeps an unattended TUI current.
	v.SetDefault("local.db_path", defaultDBPath())
	v.SetDefault("sync.interval", 2*time.Minute)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	// Env var overrides
	v.SetEnvPrefix("PROJECTS")
	_ = v.BindEnv("local.db_path", "PROJECTS_DB")
	_ = v.BindEnv("sync.enabled", "PROJECTS_SYNC")
	_ = v.BindEnv("sync.api_url", "PROJECTS_SYNC_URL")
	_ = v.BindEnv("sync.token_command", "PROJECTS_SYNC_TOKEN_COMMAND")
	_ = v.BindEnv("sync.interval", "PROJECTS_SYNC_INTERVAL")

	// ErrorUnused, so a key the tool does not read is refused rather than dropped.
	// viper decodes the merged settings map, and every default set above maps to a
	// field below, so the only keys this can reject are ones the file supplied and
	// the struct has no home for.
	//
	// Without it a misspelled key and an unset one are the same answer. A config
	// naming its registry under a key the tool does not read would fall back to the
	// data directory, find no registry there, and lift the ban with nothing
	// reporting why.
	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc()), func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Sync.Enabled && cfg.Sync.APIURL == "" {
		return nil, fmt.Errorf("sync.api_url is required when sync is enabled (set in config or PROJECTS_SYNC_URL)")
	}

	// A token command answers for the URL beside it. A URL from the environment
	// names an API the config file does not describe, so the file's command
	// never pairs with it: a dev .envrc overriding the URL would otherwise send a
	// production token to the dev API. PROJECTS_SYNC_TOKEN_COMMAND is then the only
	// source, and unset means no token.
	if os.Getenv("PROJECTS_SYNC_URL") != "" {
		cfg.Sync.TokenCommand = os.Getenv("PROJECTS_SYNC_TOKEN_COMMAND")
	}
	if cfg.Sync.TokenCommand != "" && strings.TrimSpace(cfg.Sync.TokenCommand) == "" {
		return nil, fmt.Errorf("sync.token_command names no command")
	}

	// time.NewTicker panics on a non-positive duration, and a config typo must not
	// take the TUI down. Disabling automatic sync is what sync.enabled is for.
	if cfg.Sync.Interval < minSyncInterval {
		cfg.Sync.Interval = minSyncInterval
	}

	return &cfg, nil
}

// minSyncInterval floors the reconcile period. A full pull is 2+2N requests, so
// anything shorter is a self-inflicted denial of service on the API.
const minSyncInterval = 15 * time.Second

// newViper points a fresh viper at the tool's config file. Shared by both readers
// here so they cannot drift on where that file is.
//
// Config file: $XDG_CONFIG_HOME/projects/config.toml or ~/.config/projects/config.toml.
// Use XDG explicitly rather than Go's UserConfigDir, which returns
// ~/Library/Application Support on macOS — not where CLI tools put config.
func newViper() *viper.Viper {
	v := viper.New()
	v.AddConfigPath(filepath.Join(userConfigDir(), toolName))
	v.SetConfigName("config")
	v.SetConfigType("toml")
	return v
}

// ConfiguredReposRegistry is the repos_registry key with a leading ~ expanded,
// and "" for every way reading it can fail. Absent config is one of those ways
// and is not an error: a machine keeping its registry where the tool expects it
// should not have to hold a file saying so.
//
// A read of its own rather than a field off Load's result, because Load
// validates. Sync enabled without an api_url is an error there and has nothing
// to do with where the registry lives, so resolving through it would turn that
// unrelated failure into an empty registry — which bans nothing and reports
// nothing.
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
