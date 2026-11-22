package config

import "fmt"

// The share of the rows git-zf may take when the screen is split: under
// minUISize its tables have no room, over maxUISize the output has none.
const (
	minUISize = 20
	maxUISize = 80
)

// UIConfig is the [ui] section: how git-zf shares the terminal with the
// output of the commands it runs.
type UIConfig struct {
	// Split keeps git-zf's screens in the top of the terminal and leaves the
	// rest to the output. False runs every screen full screen.
	Split bool `json:"split" toml:"split"`
	// Size is the percentage of the rows git-zf takes when split.
	Size int `json:"size" toml:"size"`
}

func (u *UIConfig) normalize() error {
	if u.Size < minUISize || u.Size > maxUISize {
		return fmt.Errorf("ui.size: %d is out of %d–%d", u.Size, minUISize, maxUISize)
	}

	return nil
}
