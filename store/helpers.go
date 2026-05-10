package store

// BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
func BranchFieldOrEmpty(b *BranchRow, fn func(*BranchRow) string) string {
	if b == nil {
		return "∅"
	}

	return fn(b)
}

// TrackerStatusOrNA returns *s or "N.A." when s is nil.
func TrackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}
