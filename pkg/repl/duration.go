package repl

import (
	"time"

	"github.com/prometheus/common/model"
)

// parseDuration accepts everything time.ParseDuration does (so existing inputs
// like "1.5s" or "300us" parse unchanged) plus Prometheus duration units such
// as "7d", "2w", "1y" or "1d12h", as accepted in PromQL range selectors.
func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	if md, merr := model.ParseDuration(s); merr == nil {
		return time.Duration(md), nil
	}
	return 0, err
}
