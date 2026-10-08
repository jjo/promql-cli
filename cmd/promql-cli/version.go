package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

const prometheusModule = "github.com/prometheus/prometheus"

// versionInfo is the resolved version information shown by `version` and `--version`.
type versionInfo struct {
	version, commit, date string
	goVersion, platform   string
	prometheus            string
}

func readBuildInfo() *debug.BuildInfo {
	bi, _ := debug.ReadBuildInfo()
	return bi
}

// resolveVersion prefers values injected via -ldflags and falls back to the
// build info Go stamps into binaries (module version and VCS settings), so a
// plain `go build` still reports a meaningful version. bi may be nil.
func resolveVersion(bi *debug.BuildInfo) versionInfo {
	v := versionInfo{
		version:   version,
		commit:    commit,
		date:      date,
		goVersion: runtime.Version(),
		platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi == nil {
		return v
	}
	if v.version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		v.version = bi.Main.Version
	}
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	if v.commit == "none" && settings["vcs.revision"] != "" {
		v.commit = settings["vcs.revision"]
		if settings["vcs.modified"] == "true" {
			v.commit += "-dirty"
		}
	}
	if v.date == "unknown" && settings["vcs.time"] != "" {
		v.date = settings["vcs.time"]
	}
	for _, d := range bi.Deps {
		if d.Path != prometheusModule {
			continue
		}
		v.prometheus = d.Path + " " + d.Version
		if d.Replace != nil {
			v.prometheus += " => " + d.Replace.Path + " " + d.Replace.Version
		}
	}
	return v
}

func formatVersion(v versionInfo) string {
	s := fmt.Sprintf("promql-cli %s\n  commit: %s\n  date:   %s\n  go:     %s %s\n", v.version, v.commit, v.date, v.goVersion, v.platform)
	if v.prometheus != "" {
		s += fmt.Sprintf("  prometheus: %s\n", v.prometheus)
	}
	return s
}
