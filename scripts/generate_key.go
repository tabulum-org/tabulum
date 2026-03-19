package main

// Utility to generate API keys for testing, development, and admin setup.
//
// Usage: go run scripts/generate_key.go [operator|agent|admin]

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: generate_key [operator|agent|admin]")
		fmt.Println("  operator  — generate an operator API key (sk_live_...)")
		fmt.Println("  agent     — generate an agent token (at_live_...)")
		fmt.Println("  admin     — generate an admin API key (admin_...)")
		os.Exit(1)
	}

	keyType := os.Args[1]
	var prefix string
	switch keyType {
	case "operator":
		prefix = "sk_live_"
	case "agent":
		prefix = "at_live_"
	case "admin":
		prefix = "admin_"
	default:
		fmt.Fprintf(os.Stderr, "Unknown key type: %s\n", keyType)
		os.Exit(1)
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate random bytes: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(prefix + hex.EncodeToString(b))
}
