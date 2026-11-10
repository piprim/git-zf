package tui

import "strings"

// keyHint renders one key hint: [S]tatus when the label starts with the single
// letter key, [r]-Sync otherwise.
func keyHint(key, label string) string {
	if len(key) == 1 && len(label) > 0 && strings.EqualFold(key, label[:1]) {
		return "[" + key + "]" + label[1:]
	}
	if len(label) > 0 {
		label = strings.ToUpper(label[:1]) + label[1:]
	}

	return "[" + key + "]-" + label
}

// keyHints joins key/label pairs rendered with keyHint by two spaces.
func keyHints(pairs ...string) string {
	out := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, keyHint(pairs[i], pairs[i+1]))
	}

	return strings.Join(out, "  ")
}
