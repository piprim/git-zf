// Command docgen writes one Markdown page per git-zf command into a directory,
// for the documentation site. Run it with `make docs-gen`; the test in this
// package fails when the committed pages drift from the code.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/piprim/git-zf/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func main() {
	out := flag.String("out", filepath.Join("website", "docs", "reference", "commands"), "directory to write the pages into")
	flag.Parse()

	if err := generate(*out); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

// generate writes the pages into out, after removing the git_zf*.md it holds.
// The command tree is built with HOME pointing at an empty directory and the
// working directory outside any repository, so that only the embedded
// defaults shape the help text, whoever runs it.
func generate(out string) error {
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	restore, err := isolate()
	if err != nil {
		return err
	}
	defer restore()

	root, err := cmd.GetRootCmd()
	if err != nil {
		return fmt.Errorf("build command tree: %w", err)
	}
	disableAutoGenTag(root)

	stale, err := filepath.Glob(filepath.Join(out, "git_zf*.md"))
	if err != nil {
		return err
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			return err
		}
	}

	return writeTree(root, out)
}

// writeTree writes the page of c and of every visible command under it.
func writeTree(c *cobra.Command, out string) error {
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := writeTree(sub, out); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	if err := doc.GenMarkdownCustom(c, &buf, pageName); err != nil {
		return err
	}
	name := pageName(c.CommandPath())
	body := frontMatter(name) + escapeProse(binaryName.ReplaceAllString(buf.String(), "git zf$1"))

	return os.WriteFile(filepath.Join(out, name), []byte(body), 0o644)
}

// binaryName matches the binary's name where cobra wrote it as a command
// ("git-zf issue start", "## git-zf"), not in ".git-zf.toml".
var binaryName = regexp.MustCompile(`git-zf([ \n)\]])`)

// escapeProse escapes < and > outside code fences: the pages are CommonMark,
// where a <placeholder> in prose is raw HTML and renders as nothing.
func escapeProse(body string) string {
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			lines[i] = htmlEscaper.Replace(line)
		}
	}

	return strings.Join(lines, "\n")
}

var htmlEscaper = strings.NewReplacer("<", "&lt;", ">", "&gt;")

// pageName turns a command path or cobra's link target ("git-zf_issue.md")
// into the page's file name ("git_zf_issue.md").
func pageName(s string) string {
	s = strings.TrimSuffix(s, ".md")
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.Replace(s, "git-zf", "git_zf", 1)

	return s + ".md"
}

// isolate points HOME at an empty directory and moves into it; the returned
// function undoes both and removes the directory.
func isolate() (func(), error) {
	tmp, err := os.MkdirTemp("", "git-zf-docgen-")
	if err != nil {
		return nil, err
	}
	home, hadHome := os.LookupEnv("HOME")
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("HOME", tmp); err != nil {
		return nil, err
	}
	if err := os.Chdir(tmp); err != nil {
		return nil, err
	}

	return func() {
		_ = os.Chdir(cwd)
		if hadHome {
			_ = os.Setenv("HOME", home)
		} else {
			_ = os.Unsetenv("HOME")
		}
		_ = os.RemoveAll(tmp)
	}, nil
}

func disableAutoGenTag(c *cobra.Command) {
	c.DisableAutoGenTag = true
	for _, sub := range c.Commands() {
		disableAutoGenTag(sub)
	}
}

// frontMatter returns the Docusaurus front matter of a page: the title is the
// command path ("git zf issue start"), the sidebar label drops the "git zf".
func frontMatter(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), ".md")
	title := strings.ReplaceAll(base, "_", " ")
	label := strings.TrimPrefix(title, "git zf ")

	return fmt.Sprintf("---\ntitle: %q\nsidebar_label: %q\n---\n\n", title, label)
}
