package repl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// pinHeaderPrefix introduces the first-line comment that `.save` writes when an
// evaluation time is pinned, so a saved file carries the time to evaluate at.
// It is a plain Prometheus text-format comment, ignored by any other parser.
const pinHeaderPrefix = "# promql-cli: pinat="

// pinFromHeader is the file whose header restored the current pin, "" when the
// pin was set explicitly (or there is none). Such a pin only says where that
// file's data ends, so it is dropped once newer samples are added.
var pinFromHeader string

// sampleCounts returns the per-metric sample counts, the "before" snapshot taken
// ahead of an operation that may add samples (see newSampleTimes).
func sampleCounts(storage *sstorage.SimpleStorage) map[string]int {
	counts := make(map[string]int, len(storage.Metrics))
	for name, ss := range storage.Metrics {
		counts[name] = len(ss)
	}
	return counts
}

// dropHeaderPinIfNewer unpins the evaluation time when it was restored from a
// file header and samples newer than it were added since beforeCounts. An
// explicit pin (.pinat, pinat=) is never dropped.
// SampleCounts and DropHeaderPinIfNewer let other packages (the MCP server) apply the
// same "a header-restored pin never hides newer data" rule after importing samples.
func SampleCounts(storage *sstorage.SimpleStorage) map[string]int { return sampleCounts(storage) }

// DropHeaderPinIfNewer is the exported form of dropHeaderPinIfNewer.
func DropHeaderPinIfNewer(out io.Writer, storage *sstorage.SimpleStorage, beforeCounts map[string]int) {
	dropHeaderPinIfNewer(out, storage, beforeCounts)
}

func dropHeaderPinIfNewer(out io.Writer, storage *sstorage.SimpleStorage, beforeCounts map[string]int) {
	if pinFromHeader == "" || pinnedEvalTime == nil {
		return
	}
	_, last, ok := newSampleTimes(storage, beforeCounts)
	if !ok || last <= pinnedEvalTime.UnixMilli() {
		return
	}
	_, _ = fmt.Fprintf(out, "Unpinned evaluation time (it was restored from %s; newer data was added)\n", pinFromHeader)
	pinnedEvalTime, pinFromHeader = nil, ""
}

// formatPinHeader returns the header line (without trailing newline) for t.
func formatPinHeader(t time.Time) string {
	return pinHeaderPrefix + t.UTC().Format(pinTimeLayout)
}

// ReadPinHeader returns the pinned evaluation time recorded in the leading
// comment lines of the file at path, if any.
func ReadPinHeader(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break // header lives in the leading comment block only
		}
		if v, ok := strings.CutPrefix(line, pinHeaderPrefix); ok {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
			if err != nil {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// ParsePinatArg scans args for pinat=... and returns (value, present).
func ParsePinatArg(args []string) (string, bool) {
	for _, a := range args {
		if !strings.HasPrefix(strings.ToLower(a), "pinat=") {
			continue
		}
		return strings.Trim(strings.TrimSpace(a[len("pinat="):]), " \"'"), true
	}
	return "", false
}

// saveEvalTime returns the time `.save` records in the pinat header: the pinned
// evaluation time, else the newest sample among the series being saved.
func saveEvalTime(storage *sstorage.SimpleStorage, re *regexp.Regexp) (time.Time, bool) {
	if pinnedEvalTime != nil {
		return *pinnedEvalTime, true
	}
	if ms, ok := storage.LatestTimestamp(re); ok {
		return time.UnixMilli(ms).UTC(), true
	}
	return time.Time{}, false
}

// newSampleTimes returns the oldest and newest timestamps among samples added
// since beforeCounts was captured.
func newSampleTimes(storage *sstorage.SimpleStorage, beforeCounts map[string]int) (first, last int64, ok bool) {
	for name, samples := range storage.Metrics {
		start := beforeCounts[name]
		if start < 0 || start > len(samples) {
			start = 0
		}
		for _, s := range samples[start:] {
			if !ok || s.Timestamp < first {
				first = s.Timestamp
			}
			if !ok || s.Timestamp > last {
				last = s.Timestamp
			}
			ok = true
		}
	}
	return first, last, ok
}

// ApplyLoadPin updates the pinned evaluation time after a file was loaded.
//
// pinat is the value of the pinat= option ("" when absent, hasPinat false):
// last/first pin to the newest/oldest sample added since beforeCounts, none
// leaves the pin alone and ignores any header, anything else follows the
// `.pinat` command grammar. Without pinat, a `# promql-cli: pinat=` header of
// the file is restored, unless tsRewritten (timestamps were rewritten on load,
// making the saved time meaningless). A pin restored from a header is dropped
// again when a later load without header (or pinat=) adds newer samples.
// Errors leave the previous pin unchanged.
// Messages go to out.
func ApplyLoadPin(out io.Writer, storage *sstorage.SimpleStorage, beforeCounts map[string]int, path, pinat string, hasPinat, tsRewritten bool) {
	if !hasPinat {
		t, ok := ReadPinHeader(path)
		switch {
		case ok && !tsRewritten:
			pinnedEvalTime, pinFromHeader = &t, path
			_, _ = fmt.Fprintf(out, "Pinned evaluation time: %s (restored from %s)\n", t.UTC().Format(pinTimeLayout), path)
		case ok:
			_, _ = fmt.Fprintf(out, "Note: %s has a saved pinat=%s, not restored because timestamp= rewrote the samples\n", path, t.UTC().Format(pinTimeLayout))
			dropHeaderPinIfNewer(out, storage, beforeCounts)
		default:
			dropHeaderPinIfNewer(out, storage, beforeCounts)
		}
		return
	}

	var t time.Time
	switch strings.ToLower(pinat) {
	case "none":
		return
	case "last", "first":
		first, last, ok := newSampleTimes(storage, beforeCounts)
		if !ok {
			_, _ = fmt.Fprintf(out, "pinat=%s: no samples loaded from %s; evaluation time unchanged\n", pinat, path)
			return
		}
		if strings.EqualFold(pinat, "last") {
			t = time.UnixMilli(last)
		} else {
			t = time.UnixMilli(first)
		}
	case "":
		_, _ = fmt.Fprintln(out, "Invalid pinat= value (empty); evaluation time unchanged")
		return
	case "now":
		t = time.Now()
	default:
		if pt, err := parseEvalTime(pinat); err == nil {
			t = pt
		} else {
			lt, _, selErr := latestSampleTime(storage, pinat)
			if selErr != nil {
				_, _ = fmt.Fprintf(out, "Invalid pinat=%q: not a time (%v), nor a usable metric selector (%v); evaluation time unchanged\n", pinat, err, selErr)
				return
			}
			t = lt
		}
	}
	pinnedEvalTime, pinFromHeader = &t, ""
	_, _ = fmt.Fprintf(out, "Pinned evaluation time: %s (from pinat=%s)\n", t.UTC().Format(pinTimeLayout), pinat)
}

// EvalTimeOrNow returns the pinned evaluation time when set, else time.Now().
// Exported for one-shot CLI evaluation paths outside the REPL.
func EvalTimeOrNow() time.Time {
	if pinnedEvalTime != nil {
		return *pinnedEvalTime
	}
	return time.Now()
}

// PinnedEvalTime returns the pinned evaluation time, if one is set.
func PinnedEvalTime() (time.Time, bool) {
	if pinnedEvalTime == nil {
		return time.Time{}, false
	}
	return *pinnedEvalTime, true
}

// ParseEvalTime parses a time as accepted by .at and .pinat: now, now-5m,
// RFC3339 or unix seconds/millis.
func ParseEvalTime(s string) (time.Time, error) { return parseEvalTime(s) }
