package commit

import (
	"bytes"
	"fmt"
	"log"
	"slices"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tui"
)

// FillOutForm presents the commit TUI form.
// Group 1: type select + commit message fields (scope, subject, body, footer).
// Group 2: commit options (author, all, amend, no-verify, signoff, allow-empty).
//
//	Group 2 is skipped when defaults.AnyOptionSet() is true (flags were passed).
//
// Returns the assembled commit message bytes and the (possibly user-modified) options.
func FillOutForm(cfg config.AppConfig, defaults tui.CommitOption) ([]byte, tui.CommitOption, error) {
	form, extractMsg, extractOpts := loadForm(cfg, defaults)
	tmplText := cfg.CommitMessage.Template

	if err := form.Run(); err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
	}

	answers := extractMsg()
	opts := extractOpts()

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, answers); err != nil {
		log.Printf("assemble failed, err=%v\n", err)

		return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
	}

	return buf.Bytes(), opts, nil
}

// BuildAuthorList deduplicates all (may contain duplicates), sorts alphabetically,
// then prepends current as the first entry (removing it from its sorted position if present).
// If current is empty, the sorted deduplicated list is returned as-is.
func BuildAuthorList(all []string, current string) []string {
	seen := make(map[string]struct{})
	var unique []string

	for _, a := range all {
		if _, ok := seen[a]; !ok {
			seen[a] = struct{}{}
			unique = append(unique, a)
		}
	}

	slices.Sort(unique)

	if current == "" {
		return unique
	}

	filtered := make([]string, 0, len(unique))
	for _, a := range unique {
		if a != current {
			filtered = append(filtered, a)
		}
	}

	return append([]string{current}, filtered...)
}

// assembleMessage trims whitespace from all string answers, then executes tmplText writing the result to buf.
func assembleMessage(buf *bytes.Buffer, tmplText string, answers map[string]any) error {
	tmpl, err := template.New("").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	for k, v := range answers {
		if s, ok := v.(string); ok {
			answers[k] = strings.TrimSpace(s)
		}
	}

	if err := tmpl.Execute(buf, answers); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func loadForm(
	cfg config.AppConfig,
	defaults tui.CommitOption,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	log.Printf("message tmpl: %s", cfg.CommitMessage.Template)

	var selectedType string

	extractMsg = func() map[string]any {
		m := make(map[string]any, len(cfg.CommitMessage.Items)+1)
		m["type"] = selectedType

		for i := range cfg.CommitMessage.Items {
			m[cfg.CommitMessage.Items[i].Name] = cfg.CommitMessage.Items[i].Value
		}

		return m
	}

	groups := []*huh.Group{tui.CommitMessageGroup(cfg.CommitTypes, cfg.CommitMessage.Items, &selectedType)}

	opts := defaults
	if !defaults.AnyOptionSet() {
		groups = append(groups, tui.CommitOptionsGroup(&opts))
	}

	extractOpts = func() tui.CommitOption { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts
}
