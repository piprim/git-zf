package version

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

type Version struct {
	version string
	name    string
}

func New(version, name string) Version {
	return Version{version: version, name: name}
}

type info struct {
	time, arch, os, revision string
	dirty                    bool
}

func (v Version) buildInfo() string {
	vinfo := vcsInfo()
	if vinfo == nil || vinfo.revision == "" {
		return v.version
	}

	dirtyStr := ""
	if vinfo.dirty {
		dirtyStr = "-dirty"
	}

	return fmt.Sprintf(`
Name: %s
Version: %s
Arch: %s
OS: %s
Revision: %s%s
Built at: %s
`, v.name, v.version, vinfo.arch, vinfo.os, vinfo.revision, dirtyStr, vinfo.time)
}

func vcsInfo() *info {
	dinfo, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	out := new(info)

	for _, s := range dinfo.Settings {
		switch s.Key {
		case "GOARCH":
			out.arch = s.Value
		case "GOOS":
			out.os = s.Value
		case "vcs.revision":
			out.revision = s.Value
		case "vcs.time":
			out.time = s.Value
		case "vcs.modified":
			out.dirty = s.Value == "true"
		}
	}

	return out
}

func (v Version) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information and quit",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(v.buildInfo())
		},
	}
}
