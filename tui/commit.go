package tui

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/piprim/git-zf/config"
)

// CommitOption holds commit option values for the commit options group of the TUI.
// Used both as flag-derived defaults (input) and as user selections (output).
type CommitOption struct {
	Skip       bool
	Authors    []string
	Author     string
	All        bool
	Amend      bool
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
}

var (
	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			PaddingLeft(1)
	titler = cases.Title(language.English, cases.NoLower)
)

// CommitMessageGroup builds the first commit form group.
// The type select is always first, populated from commitTypes and writing into selectedType.
// Remaining fields come from items (scope, subject, body, footer).
func CommitMessageGroup(commitTypes []config.CommitTypeOption, items []config.CommitItem, selectedType *string) *huh.Group {
	requireValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("required")
		}

		return nil
	}

	typeOpts := make([]huh.Option[string], len(commitTypes))
	for i, ct := range commitTypes {
		typeOpts[i] = huh.NewOption(titleCase(ct.Name)+"\n"+descStyle.Render(ct.Desc), ct.Name)
	}
	if len(typeOpts) == 0 {
		typeOpts = []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	msgFields := make([]huh.Field, 0, len(items)+1)
	msgFields = append(msgFields,
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(selectedType),
	)

	for i := range items {
		f := &items[i]
		switch f.Form {
		case "select":
			// For select items, Desc carries the prompt text (unlike input/multiline
			// where Name is title-cased and Desc is used as placeholder).
			opts := make([]huh.Option[string], len(f.Options))
			for j, opt := range f.Options {
				name := titleCase(opt.Name)
				opts[j] = huh.NewOption(name+"\n"+descStyle.Render(opt.Desc), opt.Name)
			}
			sel := huh.NewSelect[string]().
				Title(f.Desc).
				Options(opts...).
				Value(&f.Value)
			if f.Required {
				sel = sel.Validate(requireValidator)
			}
			msgFields = append(msgFields, sel)
		case "input":
			inp := huh.NewInput().
				Title(titleCase(f.Name) + ":").
				Placeholder(f.Desc).
				Value(&f.Value)
			if f.Required {
				inp = inp.Validate(requireValidator)
			}
			msgFields = append(msgFields, inp)
		case "multiline":
			txt := huh.NewText().
				Lines(2).
				Title(titleCase(f.Name)).
				Placeholder(f.Desc).
				Value(&f.Value)
			if f.Required {
				txt = txt.Validate(requireValidator)
			}
			msgFields = append(msgFields, txt)
		default:
			slog.Warn("unknown form type, skipping", "form", f.Form, "field", f.Name)
		}
	}

	msgFields = append(msgFields,
		huh.NewNote().Description("ctrl+r  pick from history"),
	)

	return huh.NewGroup(msgFields...)
}

// CommitOptionsGroup builds the second commit form group (author + commit flags).
// opt is written directly by the form fields.
func CommitOptionsGroup(opt *CommitOption) *huh.Group {
	authorOpts := make([]huh.Option[string], 0, len(opt.Authors))
	for _, a := range opt.Authors {
		authorOpts = append(authorOpts, huh.NewOption(a, a))
	}
	if len(authorOpts) == 0 {
		authorOpts = []huh.Option[string]{huh.NewOption("(no authors found)", "")}
	}

	if opt.Author == "" && len(opt.Authors) > 0 {
		opt.Author = opt.Authors[0]
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Author:").
			Options(authorOpts...).
			Value(&opt.Author),
		huh.NewConfirm().Title("Stage all tracked modified/deleted files? (--all)").Value(&opt.All),
		huh.NewConfirm().Title("Amend last commit? (--amend)").Value(&opt.Amend),
		huh.NewConfirm().Title("Skip hooks? (--no-verify)").Value(&opt.NoVerify),
		huh.NewConfirm().Title("Add Signed-off-by trailer? (--signoff)").Value(&opt.Signoff),
		huh.NewConfirm().Title("Allow empty commit? (--allow-empty)").Value(&opt.AllowEmpty),
	)
}

func titleCase(s string) string {
	return titler.String(s)
}
