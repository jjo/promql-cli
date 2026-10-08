package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func withLdflags(t *testing.T, v, c, d string) {
	t.Helper()
	ov, oc, od := version, commit, date
	version, commit, date = v, c, d
	t.Cleanup(func() { version, commit, date = ov, oc, od })
}

func fakeBuildInfo() *debug.BuildInfo {
	return &debug.BuildInfo{
		Main: debug.Module{Version: "v0.6.1-0.20261007220204-09d45fae25f0+dirty"},
		Deps: []*debug.Module{{
			Path: prometheusModule, Version: "v0.315.0",
			Replace: &debug.Module{Path: "github.com/jjo/prometheus", Version: "v1.2.3"},
		}},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "09d45fae25f0"},
			{Key: "vcs.time", Value: "2026-10-07T22:02:04Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
}

func TestResolveVersionLdflagsWin(t *testing.T) {
	withLdflags(t, "v1.0.0", "abc", "2026-01-01")
	got := resolveVersion(fakeBuildInfo())
	if got.version != "v1.0.0" || got.commit != "abc" || got.date != "2026-01-01" {
		t.Errorf("ldflags values not preferred: %+v", got)
	}
}

func TestResolveVersionBuildInfoFallback(t *testing.T) {
	withLdflags(t, "dev", "none", "unknown")
	got := resolveVersion(fakeBuildInfo())
	if got.version != "v0.6.1-0.20261007220204-09d45fae25f0+dirty" {
		t.Errorf("version = %q", got.version)
	}
	if got.commit != "09d45fae25f0-dirty" || got.date != "2026-10-07T22:02:04Z" {
		t.Errorf("commit/date = %q/%q", got.commit, got.date)
	}
	if !strings.Contains(got.prometheus, "=> github.com/jjo/prometheus v1.2.3") {
		t.Errorf("prometheus = %q", got.prometheus)
	}
	bi := fakeBuildInfo()
	bi.Settings[2].Value = "false"
	if c := resolveVersion(bi).commit; c != "09d45fae25f0" {
		t.Errorf("clean commit = %q", c)
	}
}

func TestResolveVersionNothingAvailable(t *testing.T) {
	withLdflags(t, "dev", "none", "unknown")
	for _, bi := range []*debug.BuildInfo{nil, {Main: debug.Module{Version: "(devel)"}}} {
		got := resolveVersion(bi)
		if got.version != "dev" || got.commit != "none" || got.date != "unknown" || got.prometheus != "" {
			t.Errorf("want defaults, got %+v", got)
		}
	}
	if !strings.HasPrefix(formatVersion(resolveVersion(nil)), "promql-cli dev\n  commit: none\n  date:   unknown\n") {
		t.Error("unexpected layout")
	}
}
