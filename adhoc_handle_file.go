package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func handleAdhocHistory(query string, storage *SimpleStorage) bool {
	fields := strings.Fields(query)
	var n int = -1
	if len(fields) == 2 {
		if v, err := strconv.Atoi(fields[1]); err == nil && v > 0 {
			n = v
		} else {
			fmt.Println("Usage: .history [N]")
			return true
		}
	} else if len(fields) > 2 {
		fmt.Println("Usage: .history [N]")
		return true
	}
	// Prefer in-memory history when available (prompt backend)
	entries := getInMemoryHistory()
	if len(entries) == 0 {
		// Fallback to file
		path := getHistoryFilePath()
		entries = loadHistoryFromFile(path)
	}
	if len(entries) == 0 {
		fmt.Println("No history available")
		return true
	}
	start := 0
	if n > 0 && n < len(entries) {
		start = len(entries) - n
	}
	for i := start; i < len(entries); i++ {
		fmt.Println(entries[i])
	}
	return true
}

func handleAdhocSave(query string, storage *SimpleStorage) bool {
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

func handleAdhocLoad(query string, storage *SimpleStorage) bool {
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
	beforeMetrics := len(storage.metrics)
	beforeSamples := 0
	for _, ss := range storage.metrics {
		beforeSamples += len(ss)
	}
	if err := storage.LoadFromReader(f); err != nil {
		fmt.Printf("Failed to load metrics from %s: %v\n", path, err)
		return true
	}
	afterMetrics := len(storage.metrics)
	afterSamples := 0
	for _, ss := range storage.metrics {
		afterSamples += len(ss)
	}
	fmt.Printf("Loaded %s: +%d metrics, +%d samples (total: %d metrics, %d samples)\n", path, afterMetrics-beforeMetrics, afterSamples-beforeSamples, afterMetrics, afterSamples)

	// Refresh metrics cache for autocompletion if using prompt backend
	if refreshMetricsCache != nil {
		refreshMetricsCache(storage)
	}

	return true
}
