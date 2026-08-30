package main

import (
	"encoding/json"
	"fmt"
	"os"

	"matchSystem/internal/simulator"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./Skills/create-match-rule-json/scripts/validate_rule.go <rule-json-path>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read rule JSON: %v\n", err)
		os.Exit(2)
	}

	report := simulator.ValidateRuleJSON(data)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "encode validation report: %v\n", err)
		os.Exit(2)
	}
	if !report.Valid {
		os.Exit(1)
	}
}
