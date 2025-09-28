package main

import (
	"fmt"
	"strings"
	"time"
)

func handleAdhocPinAt(query string, storage *SimpleStorage) bool {
	arg := strings.TrimSpace(strings.TrimPrefix(query, ".pinat"))
	arg = strings.Trim(arg, " \"'")
	if arg == "" {
		if pinnedEvalTime == nil {
			fmt.Println("Pinned evaluation time: none")
		} else {
			fmt.Printf("Pinned evaluation time: %s\n", pinnedEvalTime.UTC().Format(time.RFC3339))
		}
		return true
	}
	if strings.EqualFold(arg, "remove") {
		pinnedEvalTime = nil
		fmt.Println("Pinned evaluation time: removed")
		return true
	}
	var t time.Time
	var err error
	if strings.EqualFold(arg, "now") {
		t = time.Now()
	} else {
		t, err = parseEvalTime(arg)
		if err != nil {
			fmt.Printf("Invalid time %q: %v\n", arg, err)
			return true
		}
	}
	pinnedEvalTime = &t
	fmt.Printf("Pinned evaluation time: %s\n", t.UTC().Format(time.RFC3339))
	return true
}
