package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tabulum/tabulum/internal/kernel"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to kernel configuration file")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("Tabulum kernel %s", kernel.Version())

	k, err := kernel.New(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize kernel: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- k.Start()
	}()

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %s, shutting down...", sig)
	case err := <-errCh:
		if err != nil {
			log.Printf("Server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := k.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
		os.Exit(1)
	}
}
