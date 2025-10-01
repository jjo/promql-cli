package repl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// promAPIResponse is a minimal struct to parse Prometheus HTTP API responses (vector/matrix/scalar)
type promAPIResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     []promAPISeries `json:"result"`
	} `json:"data"`
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
}

// promAPISeries represents one series in the result
type promAPISeries struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`  // vector/scalar
	Values [][2]any          `json:"values"` // matrix
}

func handleAdhocScrape(query string, storage *sstorage.SimpleStorage) bool {
	args := strings.Fields(query)
	if len(args) < 2 {
		fmt.Println("Usage: .scrape <URI> [metrics_regex] [count] [delay]")
		fmt.Println("Examples: .scrape http://localhost:9100/metrics | .scrape http://localhost:9100/metrics '^(up|process_.*)$' 3 5s")
		return true
	}
	uri := args[1]
	var regexStr string
	count := 1
	delay := 10 * time.Second
	countSet := false
	delaySet := false
	for _, tok := range args[2:] {
		if !countSet {
			if n, err := strconv.Atoi(tok); err == nil {
				count = n
				if count < 1 {
					count = 1
				}
				countSet = true
				continue
			}
		}
		if !delaySet {
			if d, err := time.ParseDuration(tok); err == nil {
				delay = d
				if delay < 0 {
					delay = 0
				}
				delaySet = true
				continue
			}
		}
		if regexStr == "" {
			regexStr = strings.Trim(tok, "\"'")
			continue
		}
	}
	var re *regexp.Regexp
	var reErr error
	if strings.TrimSpace(regexStr) != "" {
		re, reErr = regexp.Compile(regexStr)
		if reErr != nil {
			fmt.Printf("Invalid metrics_regex %q: %v\n", regexStr, reErr)
			return true
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	for i := 0; i < count; i++ {
		beforeMetrics := len(storage.Metrics)
		beforeSamples := 0
		for _, ss := range storage.Metrics {
			beforeSamples += len(ss)
		}

		resp, err := client.Get(uri)
		if err != nil {
			fmt.Printf("Failed to scrape %s: %v\n", uri, err)
			return true
		}
		func() {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				fmt.Printf("Failed to scrape %s: HTTP %d\n", uri, resp.StatusCode)
				return
			}
			if re != nil {
				if err := storage.LoadFromReaderWithFilter(resp.Body, func(name string) bool { return re.MatchString(name) }); err != nil {
					fmt.Printf("Failed to parse metrics from %s: %v\n", uri, err)
					return
				}
			} else {
				if err := storage.LoadFromReader(resp.Body); err != nil {
					fmt.Printf("Failed to parse metrics from %s: %v\n", uri, err)
					return
				}
			}
		}()

		afterMetrics, afterSamples := storeTotals(storage)
		fmt.Printf("Scraped %s (%d/%d): +%d metrics, +%d samples (total: %d metrics, %d samples)\n",
			uri, i+1, count, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

		// Evaluate active rules after each scrape update
		if added, alerts, err := EvaluateActiveRules(storage); err != nil {
			fmt.Printf("Rules evaluation failed: %v\n", err)
		} else if added > 0 || alerts > 0 {
			fmt.Printf("Rules: added %d samples; %d alerts\n", added, alerts)
		}

		if i < count-1 && delay > 0 {
			time.Sleep(delay)
		}
	}

	// Refresh metrics cache for autocompletion if using prompt backend
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}

// handleAdhocPromScrapeCommand parses and executes .prom_scrape, importing results from a remote Prometheus API.
// Syntax: .prom_scrape <PROM_API_URI> 'query' [count] [delay]
func handleAdhocPromScrapeCommand(input string, storage *sstorage.SimpleStorage) bool {
	trim := strings.TrimSpace(input)
	if !strings.HasPrefix(trim, ".prom_scrape") {
		return false
	}
	// Remove command token
	rest := strings.TrimSpace(strings.TrimPrefix(trim, ".prom_scrape"))
	if rest == "" {
		fmt.Println("Usage: .prom_scrape <PROM_API_URI> 'query' [count] [delay] [auth=basic|mimir] [user=...] [pass=...] [org_id=...] [api_key=...]")
		return true
	}
	// Parse: URI, quoted or unquoted query, optional N and DELAY + auth KVs
	uri, q, count, delay, authMode, user, pass, orgID, apiKey, err := parsePromScrapeArgs(rest)
	if err != nil {
		fmt.Printf(".prom_scrape: %v\n", err)
		fmt.Println("Usage: .prom_scrape <PROM_API_URI> 'query' [count] [delay] [auth=basic|mimir] [user=...] [pass=...] [org_id=...] [api_key=...]")
		return true
	}
	if count <= 0 {
		count = 1
	}
	if delay < 0 {
		delay = 0
	}

	client := &http.Client{Timeout: 60 * time.Second}
	endpoint := buildPromQueryEndpoint(uri)
	for i := 0; i < count; i++ {
		// Build GET request to /api/v1/query?query=...
		u, err := url.Parse(endpoint)
		if err != nil {
			fmt.Printf("Invalid PROM_API_URI %q: %v\n", uri, err)
			return true
		}
		qv := u.Query()
		qv.Set("query", q)
		u.RawQuery = qv.Encode()
		req, _ := http.NewRequest("GET", u.String(), nil)
		// Apply auth
		applyPromAuth(req, authMode, user, pass, orgID, apiKey)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Prometheus API request failed: %v\n", err)
			return true
		}
		func() {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				// Try to read error details from response body
				var pr promAPIResponse
				if err := json.NewDecoder(resp.Body).Decode(&pr); err == nil {
					if pr.Error != "" {
						fmt.Printf("Prometheus API HTTP %d: %s (%s)\n", resp.StatusCode, pr.Error, pr.ErrorType)
					} else {
						fmt.Printf("Prometheus API HTTP %d\n", resp.StatusCode)
					}
				} else {
					fmt.Printf("Prometheus API HTTP %d\n", resp.StatusCode)
				}
				return
			}
			var pr promAPIResponse
			if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
				fmt.Printf("Failed to decode Prometheus API response: %v\n", err)
				return
			}
			if strings.ToLower(pr.Status) != "success" {
				if pr.Error != "" {
					fmt.Printf("Prometheus API error: %s (%s)\n", pr.Error, pr.ErrorType)
				} else {
					fmt.Printf("Prometheus API returned non-success status: %s\n", pr.Status)
				}
				return
			}
			// Import results
			added := importPromResultIntoStorage(storage, &pr)
			afterMetrics, afterSamples := storeTotals(storage)
			fmt.Printf("Imported from %s (%d/%d): +%d samples (total: %d metrics, %d samples)\n",
				u.Scheme+"://"+u.Host, i+1, count, added, afterMetrics, afterSamples)
			// Evaluate active rules after import
			if rAdded, rAlerts, rErr := EvaluateActiveRules(storage); rErr != nil {
				fmt.Printf("Rules evaluation failed: %v\n", rErr)
			} else if rAdded > 0 || rAlerts > 0 {
				fmt.Printf("Rules: added %d samples; %d alerts\n", rAdded, rAlerts)
			}
		}()

		if i < count-1 && delay > 0 {
			time.Sleep(delay)
		}
	}

	// Refresh metrics cache for autocompletion if using prompt backend
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}

// applyPromAuth sets HTTP headers based on auth mode and provided values.
func applyPromAuth(req *http.Request, authMode, user, pass, orgID, apiKey string) {
	mode := strings.ToLower(strings.TrimSpace(authMode))
	// Infer mode if not set
	if mode == "" {
		if user != "" || pass != "" {
			mode = "basic"
		} else if orgID != "" || apiKey != "" {
			mode = "mimir"
		}
	}
	switch mode {
	case "basic":
		if user != "" || pass != "" {
			req.SetBasicAuth(user, pass)
		}
	case "mimir":
		if orgID != "" {
			req.Header.Set("X-Scope-OrgID", orgID)
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	default:
		// no auth
	}
}

// importPromResultIntoStorage converts the API response into samples and appends them to storage.
// Returns number of samples added.
func importPromResultIntoStorage(storage *sstorage.SimpleStorage, pr *promAPIResponse) int {
	added := 0
	typeLower := strings.ToLower(pr.Data.ResultType)
	switch typeLower {
	case "vector":
		for _, s := range pr.Data.Result {
			value, ok1 := parsePromValue(s.Value[1])
			tsMs, ok2 := parsePromTimestampMillis(s.Value[0])
			if !ok1 || !ok2 {
				continue
			}
			labels := s.Metric
			if labels == nil {
				labels = map[string]string{}
			}
			if labels["__name__"] == "" {
				labels["__name__"] = "query_result"
			}
			storage.AddSample(labels, value, tsMs)
			added++
		}
	case "matrix":
		for _, s := range pr.Data.Result {
			labels := s.Metric
			if labels == nil {
				labels = map[string]string{}
			}
			if labels["__name__"] == "" {
				labels["__name__"] = "query_result"
			}
			for _, pair := range s.Values {
				v, ok1 := parsePromValue(pair[1])
				tsMs, ok2 := parsePromTimestampMillis(pair[0])
				if !ok1 || !ok2 {
					continue
				}
				storage.AddSample(labels, v, tsMs)
				added++
			}
		}
	case "scalar":
		if len(pr.Data.Result) == 0 {
			return 0
		}
		v, ok1 := parsePromValue(pr.Data.Result[0].Value[1])
		tsMs, ok2 := parsePromTimestampMillis(pr.Data.Result[0].Value[0])
		if ok1 && ok2 {
			labels := map[string]string{"__name__": "query_result"}
			storage.AddSample(labels, v, tsMs)
			added++
		}
	default:
		// unsupported type
	}
	return added
}

func parsePromValue(x any) (float64, bool) {
	switch v := x.(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func parsePromTimestampMillis(x any) (int64, bool) {
	switch v := x.(type) {
	case float64:
		return int64(v * 1000.0), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return int64(f * 1000.0), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return int64(f * 1000.0), true
	default:
		return 0, false
	}
}

// buildPromQueryEndpoint tries to construct a /api/v1/query endpoint from a base URI.
func buildPromQueryEndpoint(base string) string {
	b := strings.TrimRight(base, "/")
	// return b + "query"
	if strings.HasSuffix(b, "/query") {
		return b
	}
	if strings.HasSuffix(b, "/api/v1") {
		return b + "/query"
	}
	if strings.Contains(b, "/api/v1/") {
		// Assume it's already under /api/v1; append query if needed
		if strings.HasSuffix(b, "/") {
			return b + "query"
		}
		return b + "/query"
	}
	return b + "/api/v1/query"
}

// buildPromQueryRangeEndpoint tries to construct a /api/v1/query_range endpoint from a base URI.
func buildPromQueryRangeEndpoint(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/query_range") {
		return b
	}
	if strings.HasSuffix(b, "/api/v1") {
		return b + "/query_range"
	}
	if strings.Contains(b, "/api/v1/") {
		if strings.HasSuffix(b, "/") {
			return b + "query_range"
		}
		return b + "/query_range"
	}
	return b + "/api/v1/query_range"
}

// parsePromScrapeArgs parses rest of the command after .prom_scrape
func parsePromScrapeArgs(rest string) (uri string, query string, count int, delay time.Duration, authMode, user, pass, orgID, apiKey string, err error) {
	i := 0
	skipSpaces := func() {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
	}
	nextToken := func() (string, bool) {
		if i >= len(rest) {
			return "", false
		}
		start := i
		for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
			i++
		}
		return rest[start:i], true
	}
	skipSpaces()
	// URI: read until space
	start := i
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	if i == start {
		err = fmt.Errorf("missing PROM_API_URI")
		return uri, query, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	uri = rest[start:i]
	skipSpaces()
	if i >= len(rest) {
		err = fmt.Errorf("missing query expression")
		return uri, query, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	// Query: quoted or unquoted token
	if rest[i] == '\'' || rest[i] == '"' {
		quote := rest[i]
		i++
		qStart := i
		for i < len(rest) {
			if rest[i] == quote {
				break
			}
			i++
		}
		if i >= len(rest) {
			err = fmt.Errorf("unterminated quoted query")
			return uri, query, count, delay, authMode, user, pass, orgID, apiKey, err
		}
		query = rest[qStart:i]
		i++ // skip closing quote
	} else {
		qStart := i
		for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
			i++
		}
		query = rest[qStart:i]
	}
	skipSpaces()
	// Optional count
	count = 1
	delay = 0
	if i < len(rest) {
		// Try integer
		j := i
		for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		if n, errN := strconv.Atoi(rest[i:j]); errN == nil {
			count = n
			i = j
			skipSpaces()
		}
	}
	// Optional delay
	if i < len(rest) {
		j := i
		for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		if d, errD := time.ParseDuration(rest[i:j]); errD == nil {
			delay = d
			i = j
			skipSpaces()
		}
	}
	// Optional auth key=value tokens
	for {
		skipSpaces()
		tok, ok := nextToken()
		if !ok || tok == "" {
			break
		}
		if eq := strings.IndexByte(tok, '='); eq > 0 {
			k := strings.ToLower(strings.TrimSpace(tok[:eq]))
			v := strings.TrimSpace(tok[eq+1:])
			switch k {
			case "auth", "auth_mode":
				authMode = strings.ToLower(v)
			case "user", "username":
				user = v
			case "pass", "password":
				pass = v
			case "org_id", "orgid", "tenant", "tenant_id":
				orgID = v
			case "api_key", "apikey":
				apiKey = v
			}
		}
	}
	return uri, query, count, delay, authMode, user, pass, orgID, apiKey, err
}

// handleAdhocPromScrapeRangeCommand parses and executes .prom_scrape_range, importing results via query_range.
// Syntax: .prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay]
func handleAdhocPromScrapeRangeCommand(input string, storage *sstorage.SimpleStorage) bool {
	trim := strings.TrimSpace(input)
	if !strings.HasPrefix(trim, ".prom_scrape_range") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trim, ".prom_scrape_range"))
	if rest == "" {
		fmt.Println("Usage: .prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay] [auth=basic|mimir] [user=...] [pass=...] [org_id=...] [api_key=...]")
		return true
	}
	uri, q, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err := parsePromScrapeRangeArgs(rest)
	if err != nil {
		fmt.Printf(".prom_scrape_range: %v\n", err)
		fmt.Println("Usage: .prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay] [auth=basic|mimir] [user=...] [pass=...] [org_id=...] [api_key=...]")
		return true
	}
	if count <= 0 {
		count = 1
	}
	if delay < 0 {
		delay = 0
	}

	client := &http.Client{Timeout: 120 * time.Second}
	endpoint := buildPromQueryRangeEndpoint(uri)
	for i := 0; i < count; i++ {
		u, err := url.Parse(endpoint)
		if err != nil {
			fmt.Printf("Invalid PROM_API_URI %q: %v\n", uri, err)
			return true
		}
		qv := u.Query()
		qv.Set("query", q)
		qv.Set("start", start.UTC().Format(time.RFC3339))
		qv.Set("end", end.UTC().Format(time.RFC3339))
		qv.Set("step", step.String())
		u.RawQuery = qv.Encode()
		req, _ := http.NewRequest("GET", u.String(), nil)
		applyPromAuth(req, authMode, user, pass, orgID, apiKey)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Prometheus API request failed: %v\n", err)
			return true
		}
		func() {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				// Try to read error details from response body
				var pr promAPIResponse
				if err := json.NewDecoder(resp.Body).Decode(&pr); err == nil {
					if pr.Error != "" {
						fmt.Printf("Prometheus API HTTP %d: %s (%s)\n", resp.StatusCode, pr.Error, pr.ErrorType)
					} else {
						fmt.Printf("Prometheus API HTTP %d\n", resp.StatusCode)
					}
				} else {
					fmt.Printf("Prometheus API HTTP %d\n", resp.StatusCode)
				}
				return
			}
			var pr promAPIResponse
			if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
				fmt.Printf("Failed to decode Prometheus API response: %v\n", err)
				return
			}
			if strings.ToLower(pr.Status) != "success" {
				if pr.Error != "" {
					fmt.Printf("Prometheus API error: %s (%s)\n", pr.Error, pr.ErrorType)
				} else {
					fmt.Printf("Prometheus API returned non-success status: %s\n", pr.Status)
				}
				return
			}
			added := importPromResultIntoStorage(storage, &pr)
			afterMetrics, afterSamples := storeTotals(storage)
			fmt.Printf("Imported range from %s (%d/%d): +%d samples (total: %d metrics, %d samples)\n",
				u.Scheme+"://"+u.Host, i+1, count, added, afterMetrics, afterSamples)
			// Evaluate active rules after each range import
			if rAdded, rAlerts, rErr := EvaluateActiveRules(storage); rErr != nil {
				fmt.Printf("Rules evaluation failed: %v\n", rErr)
			} else if rAdded > 0 || rAlerts > 0 {
				fmt.Printf("Rules: added %d samples; %d alerts\n", rAdded, rAlerts)
			}
		}()

		if i < count-1 && delay > 0 {
			time.Sleep(delay)
		}
	}

	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}
	return true
}

// parsePromScrapeRangeArgs parses the args after .prom_scrape_range
func parsePromScrapeRangeArgs(rest string) (uri string, query string, start time.Time, end time.Time, step time.Duration, count int, delay time.Duration, authMode, user, pass, orgID, apiKey string, err error) {
	i := 0
	skipSpaces := func() {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
	}
	skipSpaces()
	// URI token
	startIdx := i
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	if i == startIdx {
		err = fmt.Errorf("missing PROM_API_URI")
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	uri = rest[startIdx:i]
	skipSpaces()
	// Query token (quoted or unquoted)
	if i >= len(rest) {
		err = fmt.Errorf("missing query expression")
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	if rest[i] == '\'' || rest[i] == '"' {
		quote := rest[i]
		i++
		qStart := i
		for i < len(rest) {
			if rest[i] == quote {
				break
			}
			i++
		}
		if i >= len(rest) {
			err = fmt.Errorf("unterminated quoted query")
			return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
		}
		query = rest[qStart:i]
		i++
	} else {
		qStart := i
		for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
			i++
		}
		query = rest[qStart:i]
	}
	skipSpaces()
	// start time
	if i >= len(rest) {
		err = fmt.Errorf("missing start time")
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	sStart := i
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	startStr := rest[sStart:i]
	start, err = parseEvalTime(startStr)
	if err != nil {
		err = fmt.Errorf("invalid start time %q: %v", startStr, err)
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	skipSpaces()
	// end time
	if i >= len(rest) {
		err = fmt.Errorf("missing end time")
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	eStart := i
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	endStr := rest[eStart:i]
	end, err = parseEvalTime(endStr)
	if err != nil {
		err = fmt.Errorf("invalid end time %q: %v", endStr, err)
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	skipSpaces()
	// step duration
	if i >= len(rest) {
		err = fmt.Errorf("missing step duration")
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	stStart := i
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	stepStr := rest[stStart:i]
	step, err = time.ParseDuration(stepStr)
	if err != nil {
		err = fmt.Errorf("invalid step duration %q: %v", stepStr, err)
		return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
	}
	skipSpaces()
	// optional count
	count = 1
	delay = 0
	if i < len(rest) {
		j := i
		for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		if n, errN := strconv.Atoi(rest[i:j]); errN == nil {
			count = n
			i = j
			skipSpaces()
		}
	}
	// optional delay
	if i < len(rest) {
		j := i
		for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		if d, errD := time.ParseDuration(rest[i:j]); errD == nil {
			delay = d
			i = j
			skipSpaces()
		}
	}
	// optional auth KV tokens
	for i < len(rest) {
		// read token
		startTok := i
		for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
			i++
		}
		tok := rest[startTok:i]
		skipSpaces()
		if tok == "" {
			break
		}
		if eq := strings.IndexByte(tok, '='); eq > 0 {
			k := strings.ToLower(strings.TrimSpace(tok[:eq]))
			v := strings.TrimSpace(tok[eq+1:])
			switch k {
			case "auth", "auth_mode":
				authMode = strings.ToLower(v)
			case "user", "username":
				user = v
			case "pass", "password":
				pass = v
			case "org_id", "orgid", "tenant", "tenant_id":
				orgID = v
			case "api_key", "apikey":
				apiKey = v
			}
		}
	}
	return uri, query, start, end, step, count, delay, authMode, user, pass, orgID, apiKey, err
}
