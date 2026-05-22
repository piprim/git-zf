package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

const (
	historyLimit  = 50
	maxLabelLen   = 60
	historyCmd    = "commit"
	newBlankLabel = "New blank commit"
)

// errNoHistory signals that ListCommandHistory returned no rows.
// FillOutForm catches it to re-open the form with current answers preserved
// and an inline note, instead of silently re-rendering blank.
var errNoHistory = errors.New("no commit history yet")

// historyStore is the subset of *store.Store used by FillOutForm.
// Injected as an interface so tests can supply a fake.
type historyStore interface {
	InsertCommandHistory(ctx context.Context, command string, payload any) error
	ListCommandHistory(ctx context.Context, command string, limit int) ([]store.CommandHistoryRow, error)
}

// formRunner is satisfied by *tui.FormRunner; the package-level var lets tests inject a stub.
type formRunner interface {
	WantHistory() bool
}

// runFormFn is the form runner used by FillOutForm and runHistoryPicker.
// Tests can replace it with a stub to avoid requiring a real terminal.
var runFormFn = func(form *huh.Form) (formRunner, error) {
	return tui.RunForm(form)
}

// applyPayload clones items and sets each item's Value from payload (string values only).
// Returns the clone unmodified for keys that don't match any item name.
func applyPayload(items []config.CommitItem, payload map[string]any) []config.CommitItem {
	out := slices.Clone(items)
	for k, v := range payload {
		if s, ok := v.(string); ok {
			setItemValue(out, k, s)
		}
	}

	return out
}

// historyLabel builds the picker display string for one history entry.
func historyLabel(tmplText string, row store.CommandHistoryRow) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return "", fmt.Errorf("unmarshal history payload: %w", err)
	}

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, payload); err != nil {
		return "", fmt.Errorf("assemble history label: %w", err)
	}

	subject := strings.SplitN(buf.String(), "\n", 2)[0]
	if len(subject) > maxLabelLen {
		subject = subject[:maxLabelLen]
	}

	return fmt.Sprintf("%-60s  [%s]", subject, row.CreatedAt.Format("2006-01-02 15:04")), nil
}

// runHistoryPicker presents a select of the last historyLimit history entries.
// Returns (nil, errNoHistory) when history is empty (caller shows inline note).
// Returns (nil, nil) when user picks "New blank commit".
// Returns (payload, nil) when user selects a history entry.
// Returns (nil, huh.ErrUserAborted) when the user aborts the picker (ctrl+c / esc).
// Returns (nil, err) on unexpected errors.
func runHistoryPicker(ctx context.Context, tmplText string, hs historyStore) (map[string]any, error) {
	entries, err := hs.ListCommandHistory(ctx, historyCmd, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}

	if len(entries) == 0 {
		showNoHistoryDialog()

		return nil, errNoHistory
	}

	opts := make([]huh.Option[int], 0, len(entries)+1)
	opts = append(opts, huh.NewOption(newBlankLabel, -1))

	for i, e := range entries {
		label, labelErr := historyLabel(tmplText, e)
		if labelErr != nil {
			slog.Warn("could not build history label", "error", labelErr)
			label = fmt.Sprintf("entry #%d", e.ID)
		}

		opts = append(opts, huh.NewOption(label, i))
	}

	selected := -1
	pickerForm := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Pick a past commit:").
			Options(opts...).
			Value(&selected),
	))

	_, runErr := runFormFn(pickerForm)
	if errors.Is(runErr, huh.ErrUserAborted) {
		return nil, huh.ErrUserAborted
	}
	if runErr != nil {
		return nil, fmt.Errorf("run picker: %w", runErr)
	}

	if selected == -1 {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(entries[selected].Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal selected payload: %w", err)
	}

	return payload, nil
}

// showNoHistoryDialog renders a modal "No commit history yet." note the user dismisses
// with enter/esc/ctrl+c. Any runner error is logged and swallowed; the dialog is purely
// informational and never blocks the calling flow.
func showNoHistoryDialog() {
	dialog := huh.NewForm(huh.NewGroup(
		huh.NewNote().
			Title("Commit history").
			Description("No commit history yet.\n\nPress enter to continue."),
	))

	if _, err := runFormFn(dialog); err != nil && !errors.Is(err, huh.ErrUserAborted) {
		slog.Warn("could not show no-history dialog", "error", err)
	}
}

// FillOutForm presents the commit TUI form and orchestrates the ctrl+r history flow.
// hs must not be nil; pass a *store.Store opened from store.OpenRepo.
//
// Exit conditions:
//   - form completed → saves payload to history, assembles and returns the commit message.
//   - ctrl+r         → shows history picker, then re-opens form (pre-filled or blank).
//   - ctrl+c / esc   → returns ("", zero, huh.ErrUserAborted).
func FillOutForm(
	ctx context.Context,
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hs historyStore,
	initialPrefill map[string]any,
) ([]byte, tui.CommitOption, error) {
	prefill := initialPrefill

	for {
		form, extractMsg, extractOpts := loadForm(cfg, defaults, prefill)

		runner, err := runFormFn(form)
		if err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
		}

		if runner.WantHistory() {
			prefill, err = runHistoryPicker(ctx, cfg.CommitMessage.Template, hs)
			if errors.Is(err, errNoHistory) {
				// Preserve current answers so re-opening the form is not destructive.
				prefill = extractMsg()

				continue
			}
			if err != nil {
				return nil, tui.CommitOption{}, err
			}

			continue
		}

		answers := extractMsg()
		opts := extractOpts()

		var buf bytes.Buffer
		if err := assembleMessage(&buf, cfg.CommitMessage.Template, answers); err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
		}

		if saveErr := hs.InsertCommandHistory(ctx, historyCmd, answers); saveErr != nil {
			slog.Warn("could not save commit history", "error", saveErr)
		}

		return buf.Bytes(), opts, nil
	}
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

// IssueHint carries issue context detected from the current branch.
// Zero value means no issue branch — form fields are left unchanged.
type IssueHint struct {
	IssueID    string
	BranchType string
}

// Prefill returns the issue-hint contribution to a FillOutForm prefill map.
// IssueID populates one field using the fallback chain
//
//	"scope" → "footer" (as "Refs: <id>") → "subject" (as "(<id>)").
//
// BranchType is emitted as "type" when non-empty; loadForm validates it
// against cfg.CommitTypes and silently ignores an unconfigured value.
func (h IssueHint) Prefill(items []config.CommitItem) map[string]any {
	out := make(map[string]any)

	if h.IssueID != "" {
		switch {
		case hasItem(items, "scope"):
			out["scope"] = h.IssueID
		case hasItem(items, "footer"):
			out["footer"] = "Refs: " + h.IssueID
		case hasItem(items, "subject"):
			out["subject"] = "(" + h.IssueID + ")"
		}
	}

	if h.BranchType != "" {
		out["type"] = h.BranchType
	}

	return out
}

// hasItem reports whether items contains an entry with the given Name.
func hasItem(items []config.CommitItem, name string) bool {
	for i := range items {
		if items[i].Name == name {
			return true
		}
	}

	return false
}

// setItemValue finds the first item with the given name and sets its Value.
// Reports whether a match was found.
func setItemValue(items []config.CommitItem, name, value string) bool {
	for i := range items {
		if items[i].Name == name {
			items[i].Value = value

			return true
		}
	}

	return false
}

// isValidCommitType reports whether name matches a configured commit type.
func isValidCommitType(types []config.CommitTypeOption, name string) bool {
	return slices.ContainsFunc(types, func(t config.CommitTypeOption) bool {
		return t.Name == name
	})
}

func loadForm(
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	prefill map[string]any,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	slog.Debug("message template", "template", cfg.CommitMessage.Template)

	items := slices.Clone(cfg.CommitMessage.Items)

	var selectedType string
	if prefill != nil {
		items = applyPayload(items, prefill)
		if t, ok := prefill["type"].(string); ok && isValidCommitType(cfg.CommitTypes, t) {
			selectedType = t
		}
	}

	extractMsg = func() map[string]any {
		m := make(map[string]any, len(items)+1)
		m["type"] = selectedType

		for i := range items {
			m[items[i].Name] = items[i].Value
		}

		return m
	}

	groups := []*huh.Group{tui.CommitMessageGroup(cfg.CommitTypes, items, &selectedType)}

	opts := defaults
	if !defaults.Skip {
		groups = append(groups, tui.CommitOptionsGroup(&opts))
	}

	extractOpts = func() tui.CommitOption { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts
}
