package chain

import (
	"slices"
	"testing"
)

type item struct {
	id, at  string
	parents []string
}

func node(i *item) Node { return Node{ID: i.id, Parents: i.parents, At: i.at} }

func order(items ...item) []string {
	ids := []string{}
	for _, i := range Order(items, node) {
		ids = append(ids, i.id)
	}

	return ids
}

func TestOrder(t *testing.T) {
	t.Parallel()

	root := item{id: "a", at: "2026-10-01T10:00:00Z"}
	x := item{id: "x", at: "2026-10-01T12:00:00Z", parents: []string{"a"}}
	y := item{id: "y", at: "2026-10-01T11:00:00Z", parents: []string{"a"}}

	t.Run("a child comes after its parent whatever the input order", func(t *testing.T) {
		t.Parallel()

		child := item{id: "b", at: "2026-10-01T09:00:00Z", parents: []string{"a"}}
		if got := order(child, root); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("concurrent items are ordered by At then ID", func(t *testing.T) {
		t.Parallel()

		c := item{id: "c", at: "2026-10-01T12:00:00Z", parents: []string{"a"}}
		if got := order(x, y, c, root); !slices.Equal(got, []string{"a", "y", "c", "x"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a merge comes after both of its parents even when dated earlier", func(t *testing.T) {
		t.Parallel()

		m := item{id: "0", at: "2026-10-01T08:00:00Z", parents: []string{"x", "y"}}
		if got := order(m, x, y, root); !slices.Equal(got, []string{"a", "y", "x", "0"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a parent outside the slice is ignored", func(t *testing.T) {
		t.Parallel()

		orphan := item{id: "o", at: "2026-10-01T10:00:00Z", parents: []string{"missing"}}
		if got := order(orphan); !slices.Equal(got, []string{"o"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a malformed At sorts before a valid one", func(t *testing.T) {
		t.Parallel()

		bad := item{id: "z", at: "yesterday", parents: []string{"a"}}
		if got := order(y, bad, root); !slices.Equal(got, []string{"a", "z", "y"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("an empty slice orders to an empty slice", func(t *testing.T) {
		t.Parallel()

		if got := order(); len(got) != 0 {
			t.Errorf("order = %v", got)
		}
	})
}

func TestParseAt(t *testing.T) {
	t.Parallel()

	t.Run("a valid RFC 3339 time is returned in UTC", func(t *testing.T) {
		t.Parallel()

		got := ParseAt("2026-10-01T12:00:00+02:00")
		if got.IsZero() || got.Hour() != 10 {
			t.Errorf("ParseAt = %v", got)
		}
	})

	t.Run("garbage is the zero time", func(t *testing.T) {
		t.Parallel()

		if got := ParseAt("yesterday"); !got.IsZero() {
			t.Errorf("ParseAt = %v", got)
		}
	})
}
