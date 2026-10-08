package repl

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
)

// commonLabelsEnabled controls whether, on an interactive terminal, the labels (and the
// timestamp) shared by every series of a result are printed once as a header instead of
// on each line.
// Toggled with `.common_labels on|off`; piped or captured output always keeps full labels.
var commonLabelsEnabled = true

// commonLabels returns the label pairs present with the same value in every set.
// It returns empty labels for fewer than two sets.
func commonLabels(sets []labels.Labels) labels.Labels {
	if len(sets) < 2 {
		return labels.EmptyLabels()
	}
	b := labels.NewScratchBuilder(0)
	sets[0].Range(func(l labels.Label) {
		for _, s := range sets[1:] {
			// Label values are never empty, so Get's "" means the label is absent.
			if s.Get(l.Name) != l.Value {
				return
			}
		}
		b.Add(l.Name, l.Value)
	})
	return b.Labels()
}

// labelFormatter returns the header line to print before the series (empty when there is
// none) and how each series' labels are rendered. On an interactive terminal the common
// labels collapse to "…" and, when ColorEnabled, the part of each label value that differs
// between series is underlined; otherwise labels are printed in full.
func labelFormatter(w io.Writer, sets []labels.Labels) (string, func(labels.Labels) string) {
	var header string
	var drop []string
	if !rawOutput && commonLabelsEnabled && isTerminal(w) {
		if common := commonLabels(sets); !common.IsEmpty() {
			common.Range(func(l labels.Label) { drop = append(drop, l.Name) })
			header = "# common_labels: " + common.String()
		}
	}
	var diffs map[string]affix
	if ColorEnabled(w) {
		diffs = differingAffixes(sets)
	}
	if len(drop) == 0 && len(diffs) == 0 {
		return header, func(l labels.Labels) string { return l.String() }
	}
	return header, func(l labels.Labels) string { return renderLabels(l, drop, diffs) }
}

// affix is the number of leading and trailing runes a label value shares with the same
// label of every other series.
type affix struct{ prefix, suffix int }

// differingAffixes returns, for each label present in every set with values that are not
// all equal, the rune counts of the prefix and suffix common to all those values.
func differingAffixes(sets []labels.Labels) map[string]affix {
	if len(sets) < 2 {
		return nil
	}
	diffs := map[string]affix{}
	sets[0].Range(func(l labels.Label) {
		values := [][]rune{[]rune(l.Value)}
		for _, s := range sets[1:] {
			v := s.Get(l.Name)
			if v == "" {
				return // absent from a series
			}
			values = append(values, []rune(v))
		}
		minLen, allEqual := len(values[0]), true
		for _, v := range values[1:] {
			minLen = min(minLen, len(v))
			allEqual = allEqual && string(v) == string(values[0])
		}
		if allEqual {
			return
		}
		p := 0
		for p < minLen && sameAt(values, func(v []rune) rune { return v[p] }) {
			p++
		}
		sfx := 0
		for sfx < minLen-p && sameAt(values, func(v []rune) rune { return v[len(v)-1-sfx] }) {
			sfx++
		}
		diffs[l.Name] = affix{p, sfx}
	})
	return diffs
}

// sameAt reports whether at returns the same rune for every value.
func sameAt(values [][]rune, at func([]rune) rune) bool {
	r := at(values[0])
	for _, v := range values[1:] {
		if at(v) != r {
			return false
		}
	}
	return true
}

const (
	underlineOn  = "\x1b[4m"
	underlineOff = "\x1b[24m"
)

// renderLabels formats l like labels.Labels.String, without the labels named in drop
// (shown as a leading "…") and with the differing part of the values in diffs underlined.
func renderLabels(l labels.Labels, drop []string, diffs map[string]affix) string {
	rest := l
	if len(drop) > 0 {
		rest = labels.NewBuilder(l).Del(drop...).Labels()
	}
	var parts []string
	if len(drop) > 0 {
		parts = append(parts, "…")
	}
	rest.Range(func(lbl labels.Label) {
		plain := strings.TrimSuffix(strings.TrimPrefix(labels.FromStrings(lbl.Name, lbl.Value).String(), "{"), "}")
		a, ok := diffs[lbl.Name]
		runes := []rune(lbl.Value)
		if !ok || a.prefix+a.suffix >= len(runes) {
			parts = append(parts, plain)
			return
		}
		name := plain[:len(plain)-len(strconv.Quote(lbl.Value))]
		parts = append(parts, name+`"`+quoteInner(string(runes[:a.prefix]))+
			underlineOn+quoteInner(string(runes[a.prefix:len(runes)-a.suffix]))+underlineOff+
			quoteInner(string(runes[len(runes)-a.suffix:]))+`"`)
	})
	return "{" + strings.Join(parts, ", ") + "}"
}

// quoteInner returns s quoted like strconv.Quote, without the surrounding quotes.
func quoteInner(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}

// commonTimestamp returns the formatted timestamp shared by every sample of v, so it
// can be printed once instead of on each line; "" when there are fewer than two samples,
// the timestamps differ, or factoring is off (not a terminal, --repl-raw, .common_labels off).
func commonTimestamp(w io.Writer, v promql.Vector) string {
	if rawOutput || !commonLabelsEnabled || !isTerminal(w) || len(v) < 2 {
		return ""
	}
	for _, s := range v[1:] {
		if s.T != v[0].T {
			return ""
		}
	}
	return model.Time(v[0].T).Time().Format(time.RFC3339)
}

// handleAdhocCommonLabels handles `.common_labels [on|off]`.
func handleAdhocCommonLabels(query string) bool {
	switch arg := strings.TrimSpace(strings.TrimPrefix(query, ".common_labels")); arg {
	case "":
	case "on":
		commonLabelsEnabled = true
	case "off":
		commonLabelsEnabled = false
	default:
		fmt.Println(GetAdHocCommandByName(".common_labels").Usage)
		return true
	}
	state := "off: full labels on every line"
	if rawOutput {
		state = "off: --repl-raw is set"
	} else if commonLabelsEnabled {
		state = "on: labels and timestamps shared by every series are printed once (interactive terminal only)"
	}
	fmt.Printf("Common labels: %s\n", state)
	return true
}
