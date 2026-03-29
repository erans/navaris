package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// isJSONOutput returns true when --output json was specified or stdout is
// not a TTY (piped / redirected).
func isJSONOutput() bool {
	f, _ := rootCmd.Flags().GetString("output")
	if strings.EqualFold(f, "json") {
		return true
	}
	return !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// printJSON pretty-prints data as indented JSON to stdout.
func printJSON(data any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}

// printTable prints aligned columns to stdout. Each row must have the same
// number of elements as headers.
func printTable(headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	// Compute column widths.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header.
	for i, h := range headers {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%-*s", widths[i], h)
	}
	fmt.Println()

	// Print rows.
	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Print("  ")
			}
			if i < len(widths) {
				fmt.Printf("%-*s", widths[i], cell)
			} else {
				fmt.Print(cell)
			}
		}
		fmt.Println()
	}
}

// printResult dispatches to JSON or table output depending on the
// configured output mode. rowsFn is only called when table output is used.
func printResult(data any, headers []string, rowsFn func() [][]string) {
	if isJSONOutput() {
		printJSON(data)
		return
	}
	printTable(headers, rowsFn())
}
