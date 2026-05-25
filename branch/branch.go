package branch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxSlugLen caps the slugged title length. Combined with a typical
// issue ID and type, this keeps the full branch name comfortably under
// ~100 chars even when an operator adds --variant.
const MaxSlugLen = 50

var (
	reSpaces          = regexp.MustCompile(`\s+`)
	nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]+`)
	multiHyphen       = regexp.MustCompile(`-{2,}`)
)

// Branch is the parsed shape of a git-zf branch name.
//
//	3-part name: <issueID>@<type>@<slug>             — default
//	4-part name: <issueID>@<type>@<slug>@<variant>   — opt-in via --variant
//
// Legacy branches with an 8-hex random suffix parse as 4-part with the
// suffix exposed verbatim via Variant(). The system no longer distinguishes
// legacy hex from operator-supplied labels.
type Branch struct {
	issueID string
	btype   string
	title   string
	variant string
}

// New constructs a Branch.
//
//	variant == ""  → 3-part name
//	variant != ""  → 4-part name; the variant is slugged and rejected
//	                 if the result is empty.
func New(issueID, branchType, title, variant string) (*Branch, error) {
	if branchType == "" {
		return nil, errors.New("branch type is empty")
	}

	slug := Slug(title)
	if slug == "" {
		return nil, fmt.Errorf("branch.New: title %q produces an empty slug", title)
	}

	var v string
	if variant != "" {
		v = Slug(variant)
		if v == "" {
			return nil, fmt.Errorf("branch.New: variant %q produces an empty slug", variant)
		}
	}

	return &Branch{
		issueID: issueID,
		btype:   branchType,
		title:   slug,
		variant: v,
	}, nil
}

func (b Branch) Name() string {
	if b.variant == "" {
		return b.issueID + "@" + b.btype + "@" + b.title
	}

	return b.issueID + "@" + b.btype + "@" + b.title + "@" + b.variant
}

func (b Branch) IssueID() string { return b.issueID }
func (b Branch) Title() string   { return b.title }
func (b Branch) Type() string    { return b.btype }

// Variant returns the trailing segment of a 4-part name, or "" for a
// 3-part name. The returned value may be an operator-supplied label or
// a legacy random-hex suffix; the API treats both the same way.
func (b Branch) Variant() string { return b.variant }

// Slug normalises a free-form title for use as a branch-name segment.
// The result is lowercased, alphanumeric + single hyphens only, trimmed,
// and capped at MaxSlugLen with any trailing hyphen stripped.
func Slug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = reSpaces.ReplaceAllString(s, "-")
	s = nonAlphanumHyphen.ReplaceAllString(s, "")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}

	return s
}

// Parse accepts 3- or 4-part branch names. For 3-part names, Variant() returns "".
func Parse(name string) (*Branch, error) {
	parts := strings.Split(name, "@")
	switch len(parts) {
	case 3:
		return fromParts(parts[0], parts[1], parts[2], ""), nil
	case 4:
		return fromParts(parts[0], parts[1], parts[2], parts[3]), nil
	default:
		return nil, fmt.Errorf("branch name %q: expected 3 or 4 parts, got %d", name, len(parts))
	}
}

// fromParts reconstructs a Branch from already-validated components.
// Unlike New, it does not re-slug the title or the variant.
func fromParts(issueID, branchType, title, variant string) *Branch {
	return &Branch{
		issueID: issueID,
		btype:   branchType,
		title:   title,
		variant: variant,
	}
}
