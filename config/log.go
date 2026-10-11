package config

import (
	"errors"
	"fmt"

	"github.com/google/shlex"
)

// LogCmd is one command of the `git zf log` view. Cmd is a shell command
// line starting with `git log` (the `git` may be left out); its output must
// begin every commit line with the hash, after any graph characters, for
// enter to find the commit.
type LogCmd struct {
	Name string `json:"name" toml:"name"`
	Cmd  string `json:"cmd"  toml:"cmd"`
	// Key is the digit (1-9) that switches to this command in the log view;
	// 0 is no shortcut.
	Key int `json:"key" toml:"key"`
}

// Args returns Cmd split into git arguments, without the leading `git`.
func (c LogCmd) Args() ([]string, error) {
	words, err := shlex.Split(c.Cmd)
	if err != nil {
		return nil, fmt.Errorf("log command %q: %w", c.Name, err)
	}
	if len(words) > 0 && words[0] == "git" {
		words = words[1:]
	}

	return words, nil
}

// LogConfig holds the commands of `git zf log`. A user array replaces the
// built-in commands wholesale.
type LogConfig struct {
	DefaultCmd string   `json:"default-cmd" toml:"default-cmd"`
	Cmds       []LogCmd `json:"cmds"        toml:"cmds"`
}

// DefaultIndex is the index in Cmds of the command named by DefaultCmd;
// normalize guarantees it exists.
func (l *LogConfig) DefaultIndex() int {
	for i, c := range l.Cmds {
		if c.Name == l.DefaultCmd {
			return i
		}
	}

	return 0
}

const maxLogKey = 9

func (l *LogConfig) normalize() error {
	if len(l.Cmds) == 0 {
		return errors.New("log.cmds: no command")
	}

	names := make(map[string]bool, len(l.Cmds))
	keys := make(map[int]bool, len(l.Cmds))
	for _, c := range l.Cmds {
		args, err := c.Args()
		switch {
		case c.Name == "":
			return errors.New("log.cmds: a command has no name")
		case c.Cmd == "":
			return fmt.Errorf("log.cmds: command %q has no cmd", c.Name)
		case err != nil:
			return fmt.Errorf("log.cmds: %w", err)
		case len(args) == 0 || args[0] != "log":
			return fmt.Errorf("log.cmds: command %q must be a git log command", c.Name)
		case c.Key < 0 || c.Key > maxLogKey:
			return fmt.Errorf("log.cmds: command %q: key %d is not between 0 and 9", c.Name, c.Key)
		case c.Key != 0 && keys[c.Key]:
			return fmt.Errorf("log.cmds: key %d is used twice", c.Key)
		case names[c.Name]:
			return fmt.Errorf("log.cmds: name %q is used twice", c.Name)
		}
		names[c.Name] = true
		keys[c.Key] = true
	}

	if l.DefaultCmd == "" {
		l.DefaultCmd = l.Cmds[0].Name
	}
	if !names[l.DefaultCmd] {
		return fmt.Errorf("log.default-cmd: %q names no command of log.cmds", l.DefaultCmd)
	}

	return nil
}
