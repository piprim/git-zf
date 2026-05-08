package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/mitchellh/mapstructure"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/viper"
)

const (
	progName       = "git-zf"
	configFileName = ".git-zf.json"
)

//go:embed default.json
var defaultJSON []byte

// CommitTypeOption is a single commit type entry (e.g. "feat", "fix").
type CommitTypeOption struct {
	Name string `json:"name" mapstructure:"name"`
	Desc string `json:"desc" mapstructure:"desc"`
}

// CommitItemOption is a selectable option within a CommitItem select field.
type CommitItemOption struct {
	Name string `json:"name" mapstructure:"name"`
	Desc string `json:"desc" mapstructure:"desc"`
}

// CommitItem describes one field in the commit message form.
// Value is written by the form after the user submits.
type CommitItem struct {
	Name     string             `json:"name"     mapstructure:"name"`
	Desc     string             `json:"desc"     mapstructure:"desc"`
	Form     string             `json:"form"     mapstructure:"form"`
	Required bool               `json:"required" mapstructure:"required"`
	Options  []CommitItemOption `json:"options"  mapstructure:"options"`
	Value    string             `json:"-" mapstructure:"-"` // runtime state; never serialised
}

// CommitMessageConfig holds the ordered list of form fields and the Go template
// used to assemble the commit message.
type CommitMessageConfig struct {
	Items    []CommitItem `json:"items"    mapstructure:"items"`
	Template string       `json:"template" mapstructure:"template"`
}

// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
type BranchConfig struct {
	Base string `json:"base" mapstructure:"base"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type     string   `json:"type"     mapstructure:"type"`
	URL      string   `json:"url"      mapstructure:"url"`
	Token    string   `json:"token"    mapstructure:"token"`
	Projects []string `json:"projects" mapstructure:"projects"`
}

// AppConfig is the top-level configuration for the application.
type AppConfig struct {
	ProgName      string
	CommitTypes   []CommitTypeOption  `json:"commit-types"   mapstructure:"commit-types"`
	CommitMessage CommitMessageConfig `json:"commit-message" mapstructure:"commit-message"`
	Branch        BranchConfig        `json:"branch"         mapstructure:"branch"`
	IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  mapstructure:"issue-tracker"`
}

// Load parses the embedded default.json then overlays any values present in the
// global viper instance. Each config section is handled individually so that a
// partial override (e.g. only commit-message.items) preserves unset defaults.
// viper.Sub is avoided because it silently returns nil for array-typed keys.
func Load() (*AppConfig, error) {
	var cfg AppConfig
	if err := json.Unmarshal(defaultJSON, &cfg); err != nil {
		return nil, fmt.Errorf("parse default config: %w", err)
	}

	// zeroSlice zeroes the target before decoding so that a user-supplied slice
	// fully replaces the default instead of being appended to it.
	zeroSlice := func(dc *mapstructure.DecoderConfig) { dc.ZeroFields = true }

	if viper.IsSet("commit-types") {
		if err := viper.UnmarshalKey("commit-types", &cfg.CommitTypes, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-types: %w", err)
		}
	}

	if viper.IsSet("commit-message.items") {
		if err := viper.UnmarshalKey("commit-message.items", &cfg.CommitMessage.Items, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-message.items: %w", err)
		}
	}

	if viper.IsSet("commit-message.template") {
		cfg.CommitMessage.Template = viper.GetString("commit-message.template")
	}

	if viper.IsSet("branch.base") {
		cfg.Branch.Base = viper.GetString("branch.base")
	}

	// issue-tracker is decoded without zeroSlice: Projects defaults to nil, so
	// a user-supplied slice fully replaces it without merge ambiguity.
	if viper.IsSet("issue-tracker") {
		if err := viper.UnmarshalKey("issue-tracker", &cfg.IssueTracker); err != nil {
			return nil, fmt.Errorf("unmarshal issue-tracker: %w", err)
		}
	}

	cfg.ProgName = progName

	return &cfg, nil
}

// DefaultJSON returns the raw embedded default configuration bytes.
func DefaultJSON() []byte {
	return defaultJSON
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

// RepoPath return the configuration directory path in the git repository of the
// project.
func RepoDir() string {
	client, err := git.NewClient()
	if err != nil {
		return ""
	}

	root, err := client.WorkingTreeRoot()
	if err != nil || root == "" {
		return ""
	}

	return filepath.Join(root, ".git")
}

// HomePath returns the configurtion file path in the user home directory.
func HomePath() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, configFileName), nil
}

// RepoPath return the configuration file path in the git repository of the project.
func RepoPath() string {
	repoDir := RepoDir()
	if repoDir == "" {
		return ""
	}

	return filepath.Join(repoDir, configFileName)
}
