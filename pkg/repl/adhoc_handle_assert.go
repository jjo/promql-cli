package repl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jjo/promql-cli/pkg/check"
	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// assertLookback is the engine's lookback delta (see newEngine in cmd/promql-cli).
const assertLookback = 5 * time.Minute

// handleAdhocAssert evaluates `.assert <expr>` at the current evaluation time
// (the .pinat time when set, else now): a non-empty result prints PASS, an
// empty one prints FAIL with the same explanation `promql-cli check` gives.
func handleAdhocAssert(query string, storage *sstorage.SimpleStorage) bool {
	expr := strings.TrimSpace(strings.TrimPrefix(query, ".assert"))
	if expr == "" {
		fmt.Println(GetAdHocCommandByName(".assert").Usage)
		queryFailures.Add(1) // a bare .assert in a -f file must not pass silently
		return true
	}
	if replEngine == nil {
		fmt.Println("Error: PromQL engine not available")
		queryFailures.Add(1)
		return true
	}
	at := time.Now()
	if t, ok := PinnedEvalTime(); ok {
		at = t
	}
	r := &check.Runner{Engine: replEngine, Queryable: storage, Parser: promParser, At: at, Timeout: replTimeout}
	res := r.Assert(context.Background(), expr)
	check.WriteResult(os.Stdout, res, ColorEnabled(os.Stdout))
	if res.Status != check.StatusPass {
		queryFailures.Add(1)
	}
	if res.Status == check.StatusFail {
		if hint := staleEvalHint(storage, at); hint != "" {
			fmt.Println(hint)
		}
	}
	return true
}

// staleEvalHint explains an empty result caused by evaluating past the data: when at is
// later than the newest sample plus the lookback window, nothing can be selected.
func staleEvalHint(storage *sstorage.SimpleStorage, at time.Time) string {
	ms, ok := storage.LatestTimestamp(nil)
	if !ok {
		return ""
	}
	newest := time.UnixMilli(ms).UTC()
	if !at.After(newest.Add(assertLookback)) {
		return ""
	}
	return fmt.Sprintf("      hint: evaluated at %s, but the newest sample is at %s; try .pinat <metric> (or .pinat %s)",
		at.UTC().Format(time.RFC3339), newest.Format(time.RFC3339), newest.Format(time.RFC3339))
}
