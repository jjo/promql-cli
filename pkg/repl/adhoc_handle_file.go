package repl

import (
	"fmt"
	"os"
	"strings"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

func handleAdhocSave(query string, storage *sstorage.SimpleStorage) bool {
	path := strings.TrimSpace(strings.TrimPrefix(query, ".save"))
	path = strings.Trim(path, " \"'")
	if path == "" {
		fmt.Println("Usage: .save <file.prom>")
		return true
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Failed to open %s for writing: %v\n", path, err)
		return true
	}
	defer f.Close()
	if err := storage.SaveToWriter(f); err != nil {
		fmt.Printf("Failed to save metrics to %s: %v\n", path, err)
		return true
	}
	fmt.Printf("Saved store to %s\n", path)
	return true
}

func handleAdhocLoad(query string, storage *sstorage.SimpleStorage) bool {
	path := strings.TrimSpace(strings.TrimPrefix(query, ".load"))
	path = strings.Trim(path, " \"'")
	if path == "" {
		fmt.Println("Usage: .load <file.prom>")
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("Failed to open %s: %v\n", path, err)
		return true
	}
	defer f.Close()
	beforeMetrics := len(storage.Metrics)
	beforeSamples := 0
	for _, ss := range storage.Metrics {
		beforeSamples += len(ss)
	}
	if err := storage.LoadFromReader(f); err != nil {
		fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
		return true
	}
	afterMetrics := len(storage.Metrics)
	afterSamples := 0
	for _, ss := range storage.Metrics {
		afterSamples += len(ss)
	}
	fmt.Printf("Loaded %s: +%d metrics, +%d samples (total: %d metrics, %d samples)\n", path, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

	// Refresh metrics cache for autocompletion if using prompt backend
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}
