package branch

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Add OAuth Login", "add-oauth-login"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"special!@#chars", "specialchars"},
		{"multiple   spaces", "multiple-spaces"},
		{"already-kebab", "already-kebab"},
		{"UPPERCASE", "uppercase"},
		{"feat/scope", "featscope"},
		{"", ""},
		{"!!!", ""},
		// MaxSlugLen behavior.
		{strings.Repeat("a", MaxSlugLen), strings.Repeat("a", MaxSlugLen)},         // exact cap, unchanged
		{strings.Repeat("a", MaxSlugLen+1), strings.Repeat("a", MaxSlugLen)},       // cap+1 cut mid-word, no dash to strip
		{"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj-x", "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj"}, // 51 chars; s[:50] ends in '-' which is stripped, leaving 49 chars
		{"Δelta " + strings.Repeat("a", MaxSlugLen+10), "elta-" + strings.Repeat("a", MaxSlugLen-5)}, // Δ stripped (non-ASCII), "elta-" prefix retained, a's truncated to cap
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			if got := Slug(tt.input); got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("default (no variant) produces a three-part branch name", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Add OAuth Login", "")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		parts := strings.Split(b.Name(), "@")
		if len(parts) != 3 {
			t.Fatalf("Name produced %d parts, want 3: %q", len(parts), b.Name())
		}

		if parts[0] != "ABC-42" {
			t.Errorf("parts[0] = %q, want %q", parts[0], "ABC-42")
		}
		if parts[1] != "feat" {
			t.Errorf("parts[1] = %q, want %q", parts[1], "feat")
		}
		if parts[2] != "add-oauth-login" {
			t.Errorf("parts[2] = %q, want %q", parts[2], "add-oauth-login")
		}

		if got := b.Variant(); got != "" {
			t.Errorf("Variant() = %q, want empty", got)
		}
	})

	t.Run("all-punctuation title that produces empty slug returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "feat", "!!!", ""); err == nil {
			t.Error("expected error for all-punctuation title, got nil")
		}
	})

	t.Run("empty branch type returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "", "branch title", ""); err == nil {
			t.Error("expected error for empty type, got nil")
		}
	})
}

func TestNew_variant(t *testing.T) {
	t.Parallel()

	t.Run("variant produces a four-part branch name", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Add OAuth Login", "spike")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		want := "ABC-42@feat@add-oauth-login@spike"
		if got := b.Name(); got != want {
			t.Errorf("Name() = %q, want %q", got, want)
		}
		if got := b.Variant(); got != "spike" {
			t.Errorf("Variant() = %q, want %q", got, "spike")
		}
	})

	t.Run("variant is slugged", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Title", "Approach B!")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if got := b.Variant(); got != "approach-b" {
			t.Errorf("Variant() = %q, want %q", got, "approach-b")
		}
	})

	t.Run("variant that slugs to empty returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "feat", "Title", "!!!"); err == nil {
			t.Error("expected error for variant slugging to empty, got nil")
		}
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	b, err := Parse("ABC-42@feat@add-oauth-login@550e8400")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	t.Run("issue ID", func(t *testing.T) {
		if b.issueID != "ABC-42" {
			t.Errorf("issueID = %q, want %q", b.issueID, "ABC-42")
		}
	})
	t.Run("branch type", func(t *testing.T) {
		if b.btype != "feat" {
			t.Errorf("branchType = %q, want %q", b.btype, "feat")
		}
	})
	t.Run("slug", func(t *testing.T) {
		if b.title != "add-oauth-login" {
			t.Errorf("title = %q, want %q", b.title, "add-oauth-login")
		}
	})
	t.Run("variant", func(t *testing.T) {
		if b.Variant() != "550e8400" {
			t.Errorf("Variant() = %q, want %q", b.Variant(), "550e8400")
		}
	})
}

func TestParse_invalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no @ separators", "no-separators"},
		{"two parts", "only@two"},
		{"five parts", "one@two@three@four@five"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse(c.input); err == nil {
				t.Errorf("Parse(%q) expected error, got nil", c.input)
			}
		})
	}
}

func TestParse_threeParts(t *testing.T) {
	t.Parallel()

	b, err := Parse("ABC-42@feat@add-oauth-login")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	t.Run("issue ID", func(t *testing.T) {
		if b.issueID != "ABC-42" {
			t.Errorf("issueID = %q, want %q", b.issueID, "ABC-42")
		}
	})
	t.Run("branch type", func(t *testing.T) {
		if b.btype != "feat" {
			t.Errorf("branchType = %q, want %q", b.btype, "feat")
		}
	})
	t.Run("slug", func(t *testing.T) {
		if b.title != "add-oauth-login" {
			t.Errorf("title = %q, want %q", b.title, "add-oauth-login")
		}
	})
	t.Run("Variant() is empty", func(t *testing.T) {
		if got := b.Variant(); got != "" {
			t.Errorf("Variant() = %q, want empty", got)
		}
	})
}
