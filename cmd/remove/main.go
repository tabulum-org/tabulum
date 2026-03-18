package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tabulum/tabulum/internal/remove"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "state":
		handleState(os.Args[2:])
	case "message":
		handleMessage(os.Args[2:])
	case "list":
		handleList(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Tabulum Content Removal Tool

Usage:
  remove state   --key <key> --reason <reason> --data-dir <path>
  remove message --id <uuid> --reason <reason> --data-dir <path>
  remove list    --data-dir <path>

The kernel must be stopped before removing state keys (bbolt exclusive lock).
All removals are recorded in the transparency log.
`)
}

func handleState(args []string) {
	var key, reason, dataDir string
	for i := 0; i < len(args)-1; i += 2 {
		switch args[i] {
		case "--key":
			key = args[i+1]
		case "--reason":
			reason = args[i+1]
		case "--data-dir":
			dataDir = args[i+1]
		}
	}

	if key == "" || reason == "" || dataDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --key, --reason, and --data-dir are all required")
		os.Exit(1)
	}

	stateDBPath := filepath.Join(dataDir, "state", "state.db")
	transparencyPath := filepath.Join(dataDir, "transparency.jsonl")

	fmt.Printf("Remove state key: %q\n", key)
	fmt.Printf("Reason: %s\n", reason)
	fmt.Printf("State DB: %s\n", stateDBPath)
	fmt.Printf("Transparency log: %s\n\n", transparencyPath)
	if !confirm("Proceed with removal?") {
		fmt.Println("Aborted.")
		return
	}

	if err := remove.RemoveStateKey(stateDBPath, transparencyPath, key, reason); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("State key removed. Transparency log updated.")
}

func handleMessage(args []string) {
	var id, reason, dataDir string
	for i := 0; i < len(args)-1; i += 2 {
		switch args[i] {
		case "--id":
			id = args[i+1]
		case "--reason":
			reason = args[i+1]
		case "--data-dir":
			dataDir = args[i+1]
		}
	}

	if id == "" || reason == "" || dataDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --id, --reason, and --data-dir are all required")
		os.Exit(1)
	}

	eventLogDir := filepath.Join(dataDir, "eventlog")
	transparencyPath := filepath.Join(dataDir, "transparency.jsonl")

	fmt.Printf("Redact message: %s\n", id)
	fmt.Printf("Reason: %s\n", reason)
	fmt.Printf("Event log dir: %s\n", eventLogDir)
	fmt.Printf("Transparency log: %s\n\n", transparencyPath)
	if !confirm("Proceed with redaction?") {
		fmt.Println("Aborted.")
		return
	}

	if err := remove.RedactMessage(eventLogDir, transparencyPath, id, reason); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Message redacted in event log. Transparency log updated.")
}

func handleList(args []string) {
	var dataDir string
	for i := 0; i < len(args)-1; i += 2 {
		if args[i] == "--data-dir" {
			dataDir = args[i+1]
		}
	}

	if dataDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --data-dir is required")
		os.Exit(1)
	}

	transparencyPath := filepath.Join(dataDir, "transparency.jsonl")
	entries, err := remove.ListRemovals(transparencyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("No removals recorded.")
		return
	}

	fmt.Printf("Removals (%d total):\n\n", len(entries))
	for i, e := range entries {
		fmt.Printf("[%d] %s\n", i+1, e.Timestamp.Format(time.RFC3339))
		fmt.Printf("    Action:       %s\n", e.Action)
		if e.Key != "" {
			fmt.Printf("    Key:          %s\n", e.Key)
		}
		if e.MessageID != "" {
			fmt.Printf("    Message ID:   %s\n", e.MessageID)
		}
		fmt.Printf("    Reason:       %s\n", e.Reason)
		fmt.Printf("    Performed by: %s\n", e.PerformedBy)
		fmt.Printf("    Content hash: %s\n\n", e.ContentHash)
	}
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
