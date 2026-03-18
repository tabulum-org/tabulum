package main

// Utility to generate API keys for testing and development.
// Not used in production — the kernel generates keys internally.
//
// Claude Code: implement this as a simple CLI that generates
// cryptographically random API keys in the same format the kernel uses.
// Useful for manual testing.
//
// Usage: go run scripts/generate_key.go [operator|agent]

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: generate_key [operator|agent]")
		fmt.Println("  operator  — generate an operator API key (sk_live_...)")
		fmt.Println("  agent     — generate an agent token (at_live_...)")
		os.Exit(1)
	}

	keyType := os.Args[1]
	switch keyType {
	case "operator":
		// TODO: generate 32 bytes of crypto/rand, hex encode, prefix with sk_live_
		fmt.Println("sk_live_TODO")
	case "agent":
		// TODO: generate 32 bytes of crypto/rand, hex encode, prefix with at_live_
		fmt.Println("at_live_TODO")
	default:
		fmt.Fprintf(os.Stderr, "Unknown key type: %s\n", keyType)
		os.Exit(1)
	}
}
