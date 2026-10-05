// Package chain orders the ops of a commit chain (refs/zf/issues/*,
// refs/zf/reviews/*) the same way on every clone.
package chain

import (
	"cmp"
	"slices"
	"time"
)

// Node is what Order needs to know about one op: its commit, the commits it
// was written on top of, and when it was written (RFC 3339).
type Node struct {
	ID      string
	Parents []string
	At      string
}

// ParseAt parses an op timestamp. A malformed one is the zero time.
func ParseAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// Order returns items so that every item comes after all of its parents. Items
// with no ordering between them (written concurrently on two clones) are
// ordered by At, then by ID, so every clone computes the same sequence. The
// order of items in the input does not matter. A parent that is not in items is
// ignored.
func Order[T any](items []T, node func(*T) Node) []T {
	nodes := make([]Node, len(items))
	index := make(map[string]int, len(items))
	for i := range items {
		nodes[i] = node(&items[i])
		index[nodes[i].ID] = i
	}

	pending := make([]int, len(items))
	children := make([][]int, len(items))
	for i, n := range nodes {
		for _, p := range n.Parents {
			pi, known := index[p]
			if !known {
				continue
			}
			pending[i]++
			children[pi] = append(children[pi], i)
		}
	}

	var ready []int
	for i := range nodes {
		if pending[i] == 0 {
			ready = append(ready, i)
		}
	}

	out := make([]T, 0, len(items))
	for len(ready) > 0 {
		// ponytail: re-sorts the ready set at every step, O(n² log n) worst
		// case; switch to container/heap if a chain reaches thousands of ops.
		slices.SortFunc(ready, func(a, b int) int {
			if c := ParseAt(nodes[a].At).Compare(ParseAt(nodes[b].At)); c != 0 {
				return c
			}

			return cmp.Compare(nodes[a].ID, nodes[b].ID)
		})

		next := ready[0]
		ready = ready[1:]
		out = append(out, items[next])

		for _, child := range children[next] {
			pending[child]--
			if pending[child] == 0 {
				ready = append(ready, child)
			}
		}
	}

	return out
}
