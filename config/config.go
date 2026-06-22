package config

import (
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/mitchellh/mapstructure"
	toml "github.com/pelletier/go-toml"
	"github.com/piprim/git-zf/internal/gitdir"
	"github.com/spf13/viper"
)

const (
	SubCommandName = "zf"
	ProgName       = "git-" + SubCommandName
	configFileName = ".git-zf.toml"
)

//go:embed default.toml
var defaultTOML []byte

// CommitTypeOption is a single commit type entry (e.g. "feat", "fix").
type CommitTypeOption struct {
	Name string `json:"name" toml:"name" mapstructure:"name"`
	Desc string `json:"desc" toml:"desc" mapstructure:"desc"`
}

// CommitItemOption is a selectable option within a CommitItem select field.
type CommitItemOption struct {
	Name string `json:"name" toml:"name" mapstructure:"name"`
	Desc string `json:"desc" toml:"desc" mapstructure:"desc"`
}

// CommitItem describes one field in the commit message form.
// Value is written by the form after the user submits.
type CommitItem struct {
	Name     string             `json:"name"     toml:"name"     mapstructure:"name"`
	Desc     string             `json:"desc"     toml:"desc"     mapstructure:"desc"`
	Form     string             `json:"form"     toml:"form"     mapstructure:"form"`
	Required bool               `json:"required" toml:"required" mapstructure:"required"`
	Options  []CommitItemOption `json:"options"  toml:"options"  mapstructure:"options"`
	Value    string             `json:"-"        toml:"-"        mapstructure:"-"`
}

// CommitMessageConfig holds the ordered list of form fields and the Go template
// used to assemble the commit message.
type CommitMessageConfig struct {
	Items       []CommitItem `json:"items"        toml:"items"        mapstructure:"items"`
	Template    string       `json:"template"     toml:"template"     mapstructure:"template"`
	RefFormat   string       `json:"ref-format"   toml:"ref-format"   mapstructure:"ref-format"`
	CloseFormat string       `json:"close-format" toml:"close-format" mapstructure:"close-format"`
}

// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
// Remote is the git remote name to use; empty means auto-detect.
type BranchConfig struct {
	Base        string `json:"base"          toml:"base"          mapstructure:"base"`
	Remote      string `json:"remote"        toml:"remote"        mapstructure:"remote"`
	UseWorktree *bool  `json:"use-worktree"  toml:"use-worktree"  mapstructure:"use-worktree"`
	WorktreeDir string `json:"worktree-dir"  toml:"worktree-dir"  mapstructure:"worktree-dir"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type     string   `json:"type"     toml:"type"     mapstructure:"type"`
	URL      string   `json:"url"      toml:"url"      mapstructure:"url"`
	Token    string   `json:"token"    toml:"token"    mapstructure:"token"`
	Projects []string `json:"projects" toml:"projects" mapstructure:"projects"`
}

// AppConfig is the top-level configuration for the application.
type AppConfig struct {
	ProgName      string              `json:"-" toml:"-" mapstructure:"-"`
	ConfigFile    string              `json:"-" toml:"-" mapstructure:"-"` // path the values were loaded from; "" when none
	CommitTypes   []CommitTypeOption  `json:"commit-types"   toml:"commit-types"   mapstructure:"commit-types"`
	CommitMessage CommitMessageConfig `json:"commit-message" toml:"commit-message" mapstructure:"commit-message"`
	Branch        BranchConfig        `json:"branch"         toml:"branch"         mapstructure:"branch"`
	IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  toml:"issue-tracker"  mapstructure:"issue-tracker"`
}

// Load parses the embedded default.toml then overlays any values present in the
// supplied viper instance. The viper is passed in (rather than read from the
// package global) so the load has no hidden dependency on prior package state
// and can be exercised in isolation from a unit test. Each config section is
// handled individually so that a partial override (e.g. only
// commit-message.items) preserves unset defaults. viper.Sub is avoided because
// it silently returns nil for array-typed keys.
func Load(v *viper.Viper) (*AppConfig, error) {
	var cfg AppConfig
	if err := toml.Unmarshal(defaultTOML, &cfg); err != nil {
		return nil, fmt.Errorf("parse default config: %w", err)
	}

	// zeroSlice zeroes the target before decoding so that a user-supplied slice
	// fully replaces the default instead of being appended to it.
	zeroSlice := func(dc *mapstructure.DecoderConfig) { dc.ZeroFields = true }

	if v.IsSet("commit-types") {
		if err := v.UnmarshalKey("commit-types", &cfg.CommitTypes, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-types: %w", err)
		}
	}

	if v.IsSet("commit-message.items") {
		if err := v.UnmarshalKey("commit-message.items", &cfg.CommitMessage.Items, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-message.items: %w", err)
		}
	}

	if v.IsSet("commit-message.template") {
		cfg.CommitMessage.Template = v.GetString("commit-message.template")
	}

	if v.IsSet("commit-message.ref-format") {
		cfg.CommitMessage.RefFormat = v.GetString("commit-message.ref-format")
	}

	if v.IsSet("commit-message.close-format") {
		cfg.CommitMessage.CloseFormat = v.GetString("commit-message.close-format")
	}

	if v.IsSet("branch.base") {
		cfg.Branch.Base = v.GetString("branch.base")
	}

	if v.IsSet("branch.remote") {
		cfg.Branch.Remote = v.GetString("branch.remote")
	}

	if v.IsSet("branch.use-worktree") {
		b := v.GetBool("branch.use-worktree")
		cfg.Branch.UseWorktree = &b
	}

	if v.IsSet("branch.worktree-dir") {
		cfg.Branch.WorktreeDir = v.GetString("branch.worktree-dir")
	}

	// issue-tracker is decoded without zeroSlice: Projects defaults to nil, so
	// a user-supplied slice fully replaces it without merge ambiguity.
	if v.IsSet("issue-tracker") {
		if err := v.UnmarshalKey("issue-tracker", &cfg.IssueTracker); err != nil {
			return nil, fmt.Errorf("unmarshal issue-tracker: %w", err)
		}
	}

	cfg.ProgName = ProgName
	cfg.ConfigFile = v.ConfigFileUsed()

	return &cfg, nil
}

// DefaultTOML returns the raw embedded default configuration bytes.
func DefaultTOML() []byte {
	return defaultTOML
}

// HomeDir returns the configuration directory path in the user home directory
// where live the config file.
func HomeDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	return home, nil
}

// RepoDir returns the repository's git directory path. Resolves gitfiles,
// submodules, and linked worktrees via git rev-parse. Returns "" on error.
func RepoDir() string {
	d, err := gitdir.Get()
	if err != nil {
		return ""
	}

	return d
}

// HomePath returns the configuration file path in the user home directory.
func HomePath() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, configFileName), nil
}

// RepoPath returns the configuration file path in the git repository of the project.
func RepoPath() string {
	repoDir := RepoDir()
	if repoDir == "" {
		return ""
	}

	return filepath.Join(repoDir, configFileName)
}
