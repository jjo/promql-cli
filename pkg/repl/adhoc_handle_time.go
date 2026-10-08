package repl

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/prometheus/prometheus/tsdb/chunkenc"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// pinTimeLayout keeps millisecond precision, so a pin taken from a sample
// timestamp shows the exact instant it was pinned to.
const pinTimeLayout = "2006-01-02T15:04:05.000Z07:00"

func handleAdhocPinAt(query string, storage *sstorage.SimpleStorage) bool {
	arg := strings.TrimSpace(strings.TrimPrefix(query, ".pinat"))
	arg = strings.Trim(arg, " \"'")
	if arg == "" {
		if pinnedEvalTime == nil {
			fmt.Println("Pinned evaluation time: none")
		} else {
			fmt.Printf("Pinned evaluation time: %s\n", pinnedEvalTime.UTC().Format(pinTimeLayout))
		}
		return true
	}
	if strings.EqualFold(arg, "remove") {
		pinnedEvalTime, pinFromHeader = nil, ""
		fmt.Println("Pinned evaluation time: removed")
		return true
	}
	var t time.Time
	if strings.EqualFold(arg, "now") {
		t = time.Now()
	} else if pt, err := parseEvalTime(arg); err == nil {
		t = pt
	} else {
		// Not a time: try it as a metric selector and pin to its newest sample.
		lt, n, selErr := latestSampleTime(storage, arg)
		if selErr != nil {
			fmt.Printf("Invalid .pinat argument %q: not a time (%v), nor a usable metric selector (%v)\n", arg, err, selErr)
			return true
		}
		pinnedEvalTime, pinFromHeader = &lt, ""
		fmt.Printf("Pinned evaluation time: %s (latest sample of %s, %d series)\n", lt.UTC().Format(pinTimeLayout), arg, n)
		return true
	}
	pinnedEvalTime, pinFromHeader = &t, ""
	fmt.Printf("Pinned evaluation time: %s\n", t.UTC().Format(pinTimeLayout))
	return true
}

// latestSampleTime returns the newest sample timestamp across all series
// matching selector (e.g. `node_load1` or `up{job="node"}`), and how many
// series matched.
func latestSampleTime(storage *sstorage.SimpleStorage, selector string) (time.Time, int, error) {
	if storage == nil {
		return time.Time{}, 0, errors.New("no storage loaded")
	}
	matchers, err := promParser.ParseMetricSelector(selector)
	if err != nil {
		return time.Time{}, 0, err
	}
	q, err := storage.Querier(math.MinInt64, math.MaxInt64)
	if err != nil {
		return time.Time{}, 0, err
	}
	defer func() { _ = q.Close() }()

	set := q.Select(context.Background(), false, nil, matchers...)
	var (
		latest int64 = math.MinInt64
		series int
	)
	for set.Next() {
		series++
		it := set.At().Iterator(nil)
		for it.Next() != chunkenc.ValNone {
			if ts := it.AtT(); ts > latest {
				latest = ts
			}
		}
		if err := it.Err(); err != nil {
			return time.Time{}, 0, err
		}
	}
	if err := set.Err(); err != nil {
		return time.Time{}, 0, err
	}
	if series == 0 {
		return time.Time{}, 0, fmt.Errorf("no series match %s", selector)
	}
	if latest == math.MinInt64 {
		return time.Time{}, 0, fmt.Errorf("%d series match %s but have no samples", series, selector)
	}
	return time.UnixMilli(latest), series, nil
}
