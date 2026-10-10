package completion_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/cmd/completion"
	"github.com/spf13/cobra"
)

// bashScript generates the bash completion of a root named git-zf with an
// `issue` subcommand and writes it to a file.
func bashScript(t *testing.T) string {
	t.Helper()

	root := &cobra.Command{Use: "git-zf"}
	root.AddCommand(&cobra.Command{Use: "issue", Run: func(*cobra.Command, []string) {}}, completion.Cmd())

	var out bytes.Buffer

	root.SetOut(&out)
	root.SetArgs([]string{"completion", "bash"})

	if err := root.Execute(); err != nil {
		t.Fatalf("completion bash: %v", err)
	}

	path := filepath.Join(t.TempDir(), "git-zf")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// complete sources the script and completes line as bash would, with the
// cursor at its end; it returns the candidates.
func complete(t *testing.T, script, fn, line string) []string {
	t.Helper()

	words := strings.Fields(line)
	if strings.HasSuffix(line, " ") {
		words = append(words, "")
	}

	// bash-completion 2.12 moved _init_completion to a compatibility file
	// that is not always installed; cobra's script needs it, so stand it in.
	cmd := exec.Command("bash", "-c",
		`source "$BASH_COMPLETION"
		declare -F _init_completion >/dev/null || _init_completion() { _comp_initialize "$@"; }
		source "$1"; shift; COMP_WORDS=("$@"); COMP_CWORD=$(($# - 1)); COMP_LINE=$LINE; COMP_POINT=${#LINE}; `+
			fn+`; printf '%s\n' "${COMPREPLY[@]}"`, "bash", script)
	cmd.Args = append(cmd.Args, words...)
	cmd.Env = append(os.Environ(), "LINE="+line, "BASH_COMPLETION="+bashCompletion)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}

	return strings.Fields(string(out))
}

// bashCompletion is the bash-completion library cobra's script relies on.
const bashCompletion = "/usr/share/bash-completion/bash_completion"

func TestCompletionBash_GitSubcommand(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}

	if _, err := os.Stat(bashCompletion); err != nil {
		t.Skip("bash-completion not installed")
	}

	script := bashScript(t)

	t.Run("git-zf completes its subcommands", func(t *testing.T) {
		if got := complete(t, script, "__start_git-zf", "git-zf iss"); !contains(got, "issue") {
			t.Errorf("COMPREPLY = %v, want issue", got)
		}
	})

	t.Run("git zf completes the same subcommands through _git_zf", func(t *testing.T) {
		if got := complete(t, script, "_git_zf", "git zf iss"); !contains(got, "issue") {
			t.Errorf("COMPREPLY = %v, want issue", got)
		}
	})

	t.Run("git options before zf are skipped", func(t *testing.T) {
		if got := complete(t, script, "_git_zf", "git -C /tmp zf iss"); !contains(got, "issue") {
			t.Errorf("COMPREPLY = %v, want issue", got)
		}
	})

	t.Run("an alias of zf completes as git zf", func(t *testing.T) {
		if got := complete(t, script, "_git_zf", "git z iss"); !contains(got, "issue") {
			t.Errorf("COMPREPLY = %v, want issue", got)
		}
	})

	t.Run("an empty word lists every subcommand", func(t *testing.T) {
		got := complete(t, script, "_git_zf", "git zf ")
		if !contains(got, "issue") || !contains(got, "completion") {
			t.Errorf("COMPREPLY = %v, want issue and completion", got)
		}
	})
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}

	return false
}
