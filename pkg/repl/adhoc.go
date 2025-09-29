package repl

import (
	"fmt"
	"strings"
	"time"

	sstorage "github.com/jjo/promql-cli/pkg/storage"
)

// pinnedEvalTime, when set, forces future query evaluation to use this timestamp.
// It is used by the REPL and can be controlled via the .pinat ad-hoc command.
var pinnedEvalTime *time.Time

// refreshMetricsCache is a function pointer to refresh the metrics cache for autocompletion
// It's set by the prompt backend when active
var refreshMetricsCache func(*sstorage.SimpleStorage)

// handleAdHocFunction handles special ad-hoc functions that are not part of PromQL
func handleAdHocFunction(query string, storage *sstorage.SimpleStorage) bool {
	trimmed := strings.TrimSpace(query)
	// .help: show ad-hoc commands usage
	if strings.HasPrefix(trimmed, ".help") {
		handleHelpCommand()
		return true
	}

	// .ai: AI-assisted query suggestions
	if strings.HasPrefix(trimmed, ".ai") {
		if handled := handleAdhocAI(trimmed, storage); handled {
			return true
		}
	}

	// .history: show full history or last N
	if strings.HasPrefix(trimmed, ".history") {
		if handled := handleAdhocHistory(trimmed, storage); handled {
			return true
		}
	}

	// .metrics: list metric names
	if trimmed == ".metrics" {
		if handled := handleAdhocMetrics(trimmed, storage); handled {
			return true
		}
	}

	// .stats: show totals
	if trimmed == ".stats" {
		if handled := handleAdhocStats(trimmed, storage); handled {
			return true
		}
	}

	// Handle .rules [spec]
	if strings.HasPrefix(trimmed, ".rules") {
		if handled := handleAdhocRules(trimmed, storage); handled {
			return true
		}
	}

	// Handle .labels <metric>
	if strings.HasPrefix(trimmed, ".labels") {
		if handled := handleAdhocLabels(trimmed, storage); handled {
			return true
		}
	}

	// Handle .timestamps <metric>
	if strings.HasPrefix(trimmed, ".timestamps ") || trimmed == ".timestamps" {
		if handled := handleAdhocTimestamps(trimmed, storage); handled {
			return true
		}
	}

	// Handle .drop <series regex>
	if strings.HasPrefix(trimmed, ".drop ") || trimmed == ".drop" {
		if handled := handleAdhocDrop(trimmed, storage); handled {
			return true
		}
	}

	// Handle .keep <series regex>
	if strings.HasPrefix(trimmed, ".keep ") || trimmed == ".keep" {
		if handled := handleAdhocKeep(trimmed, storage); handled {
			return true
		}
	}

	// Handle .save <file.prom>
	if strings.HasPrefix(trimmed, ".save ") || trimmed == ".save" {
		if handled := handleAdhocSave(trimmed, storage); handled {
			return true
		}
	}

	// Handle .load <file.prom>
	if strings.HasPrefix(trimmed, ".load ") || trimmed == ".load" {
		if handled := handleAdhocLoad(trimmed, storage); handled {
			return true
		}
	}

	// Handle .pinat <time|now|remove>
	if strings.HasPrefix(trimmed, ".pinat") {
		if handled := handleAdhocPinAt(trimmed, storage); handled {
			return true
		}
	}

	// Handle .prom_scrape_range <PROM_API_URI> 'query' <start> <end> <step> [count] [delay]
	if strings.HasPrefix(trimmed, ".prom_scrape_range") {
		if handled := handleAdhocPromScrapeRangeCommand(trimmed, storage); handled {
			return true
		}
	}

	// Handle .prom_scrape <PROM_API_URI> 'query' [count] [delay]
	if strings.HasPrefix(trimmed, ".prom_scrape") {
		if handled := handleAdhocPromScrapeCommand(trimmed, storage); handled {
			return true
		}
	}

	// Handle .scrape <URI> [metrics_regex] [count] [delay]
	if strings.HasPrefix(trimmed, ".scrape ") {
		if handled := handleAdhocScrape(trimmed, storage); handled {
			return true
		}
	}

	// Handle .seed <metric> [steps=N] [step=1m]
	if strings.HasPrefix(trimmed, ".seed ") {
		if handled := handleAdhocSeed(trimmed, storage); handled {
			return true
		}
	}

	return false
}

// handleHelpCommand handles the .help command
func handleHelpCommand() {
	fmt.Println("\nAd-hoc commands:")
	for _, cmd := range AdHocCommands {
		fmt.Printf("  %s\n", cmd.Usage)
		fmt.Printf("    %s\n", cmd.Description)
		if len(cmd.Examples) > 0 {
			if len(cmd.Examples) == 1 {
				fmt.Printf("    Example: %s\n", cmd.Examples[0])
			} else {
				fmt.Println("    Examples:")
				for _, ex := range cmd.Examples {
					fmt.Printf("      %s\n", ex)
				}
			}
		}
	}
	fmt.Println()
}
