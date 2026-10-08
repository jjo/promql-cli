package repl

import (
	"strings"
	"testing"
	"time"
)

func TestParsePromScrapeArgs_RejectsUnexpected(t *testing.T) {
	for in, want := range map[string]string{
		`http://p:9090/ 'up' now-1d now 1m`: ".prom_scrape_range",
		`http://p:9090/ 'up' 2 1s bogus=1`:  `unknown option "bogus=1"`,
		`http://p:9090/ 'up' 2 1s extra`:    `unexpected argument "extra"`,
	} {
		_, _, _, _, _, _, _, _, _, err := parsePromScrapeArgs(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got err %v, want it to mention %q", in, err, want)
		}
	}
}

func TestParsePromScrapeArgs_Valid(t *testing.T) {
	uri, q, count, delay, auth, user, pass, _, _, err := parsePromScrapeArgs(`http://p:9090/ 'up{a=~"x|y"}' 3 2d auth=basic user=u pass=p`)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "http://p:9090/" || q != `up{a=~"x|y"}` || count != 3 || delay != 48*time.Hour || auth != "basic" || user != "u" || pass != "p" {
		t.Fatalf("unexpected parse: %q %q %d %v %q %q %q", uri, q, count, delay, auth, user, pass)
	}
}
