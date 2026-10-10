package config

import (
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml"
	"github.com/piprim/git-zf/internal/gitdir"
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
	Name string `json:"name" toml:"name"`
	Desc string `json:"desc" toml:"desc"`
}

// CommitItemOption is a selectable option within a CommitItem select field.
type CommitItemOption struct {
	Name string `json:"name" toml:"name"`
	Desc string `json:"desc" toml:"desc"`
}

// CommitItem describes one field in the commit message form.
// Value is written by the form after the user submits.
type CommitItem struct {
	Name     string             `json:"name"     toml:"name"`
	Desc     string             `json:"desc"     toml:"desc"`
	Form     string             `json:"form"     toml:"form"`
	Required bool               `json:"required" toml:"required"`
	Options  []CommitItemOption `json:"options"  toml:"options"`
	Value    string             `json:"-"        toml:"-"`
}

// CommitMessageConfig holds the ordered list of form fields and the Go template
// used to assemble the commit message.
type CommitMessageConfig struct {
	Items       []CommitItem `json:"items"        toml:"items"`
	Template    string       `json:"template"     toml:"template"`
	RefFormat   string       `json:"ref-format"   toml:"ref-format"`
	CloseFormat string       `json:"close-format" toml:"close-format"`
}

// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
// Remote is the git remote name to use; empty means auto-detect.
type BranchConfig struct {
	Base        string `json:"base"          toml:"base"`
	Remote      string `json:"remote"        toml:"remote"`
	UseWorktree *bool  `json:"use-worktree"  toml:"use-worktree"`
	WorktreeDir string `json:"worktree-dir"  toml:"worktree-dir"`
}

// PushConfig holds settings for the post-action push proposal.
// Propose is the master switch (default true); when false the propose-to-push
// step is skipped everywhere.
type PushConfig struct {
	Propose bool `json:"propose" toml:"propose"`
}

// ReviewConfig holds review settings. RequireSigned makes `review approve`
// sign its op, and `issue close` refuse an approval whose signature git does
// not trust or that does not cover the branch tip.
type ReviewConfig struct {
	RequireSigned bool `json:"require-signed" toml:"require-signed"`
}

// TrackerProject names one tracker project twice. Name is the stable local
// name stored in the repository; Load lowercases it. ID is what the
// tracker calls the project, passed to it as written.
type TrackerProject struct {
	Name string `json:"name" toml:"name"`
	ID   string `json:"id"  toml:"id"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type   string `json:"type"   toml:"type"`
	URL    string `json:"url"    toml:"url"`
	Token  string `json:"token"  toml:"token"`
	Mirror bool   `json:"mirror" toml:"mirror"`

	Projects []TrackerProject `json:"projects" toml:"projects"`
}

// IDs returns the tracker-side names of the configured projects.
func (c *IssueTrackerConfig) IDs() []string {
	out := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		out[i] = p.ID
	}

	return out
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// normalize lowercases the names and checks the projects and the mirror
// switch.
func (c *IssueTrackerConfig) normalize() error {
	seen := make(map[string]bool, len(c.Projects))
	for i := range c.Projects {
		p := &c.Projects[i]
		p.Name = strings.ToLower(p.Name)

		switch {
		case !nameRe.MatchString(p.Name):
			return fmt.Errorf(
				"issue-tracker.projects: name %q must be letters, digits and dashes, "+
					"starting with a letter or a digit",
				p.Name)
		case p.ID == "":
			return fmt.Errorf("issue-tracker.projects: project %q has no id", p.Name)
		case seen[p.Name]:
			return fmt.Errorf("issue-tracker.projects: name %q is used twice", p.Name)
		}
		seen[p.Name] = true
	}

	if c.Mirror && (c.Type == "" || len(c.Projects) != 1) {
		return errors.New(
			"issue-tracker: mirror = true needs a tracker type and exactly one [[issue-tracker.projects]] entry")
	}

	return nil
}

const projectsKey = "issue-tracker.projects"

const oldProjectsHelp = `issue-tracker.projects is no longer a list of strings. Write one table per project:

    [[issue-tracker.projects]]
    name = "myproject"          # stable local name
    id   = "owner/repo"         # what the tracker calls it`

// unmarshalFile decodes one config file into cfg. The former string form of
// issue-tracker.projects is an error; an empty array, which older default
// configs wrote, is dropped: the decoder cannot turn it into a struct slice.
func unmarshalFile(b []byte, cfg *AppConfig) error {
	tree, err := toml.LoadBytes(b)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if old, ok := tree.Get(projectsKey).([]any); ok {
		if len(old) > 0 {
			return errors.New(oldProjectsHelp)
		}
		if err := tree.Delete(projectsKey); err != nil {
			return fmt.Errorf("drop empty projects: %w", err)
		}
	}

	if err := tree.Unmarshal(cfg); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	return nil
}

// AppConfig is the top-level configuration for the application.
type AppConfig struct {
	ProgName      string              `json:"-" toml:"-"`
	ConfigFile    string              `json:"-" toml:"-"` // path the values were loaded from; "" when none
	CommitTypes   []CommitTypeOption  `json:"commit-types"   toml:"commit-types"`
	CommitMessage CommitMessageConfig `json:"commit-message" toml:"commit-message"`
	Branch        BranchConfig        `json:"branch"         toml:"branch"`
	Push          PushConfig          `json:"push"           toml:"push"`
	Review        ReviewConfig        `json:"review"         toml:"review"`
	IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  toml:"issue-tracker"`
}

// Load parses the embedded default.toml, then overlays each existing file in
// paths in order (later files win). A file that does not exist is skipped.
// Each unmarshal merges into the same struct, so a key a file leaves out
// keeps its earlier value while a slice a file does set replaces the earlier
// one wholesale. ConfigFile records the last file that was actually read.
func Load(paths ...string) (*AppConfig, error) {
	var cfg AppConfig
	if err := toml.Unmarshal(defaultTOML, &cfg); err != nil {
		return nil, fmt.Errorf("parse default config: %w", err)
	}

	for _, path := range paths {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("no config file", "path", path)

			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := unmarshalFile(b, &cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}

		slog.Debug("loaded config", "path", path)
		cfg.ConfigFile = path
	}

	cfg.ProgName = ProgName

	if err := cfg.IssueTracker.normalize(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &cfg, nil
}

// DefaultTOML returns the raw embedded default configuration bytes.
func DefaultTOML() []byte {
	return defaultTOML
}

// HomePath returns the configuration file path in the user home directory.
func HomePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	return filepath.Join(home, configFileName), nil
}

// RepoPath returns the configuration file path in the repository's common git
// directory (gitfiles and submodules resolved via git rev-parse), or "" when
// not inside a git repository. Every linked worktree shares the one file of
// the main checkout.
func RepoPath() string {
	d, err := gitdir.Common()
	if err != nil {
		return ""
	}

	return filepath.Join(d, configFileName)
}
