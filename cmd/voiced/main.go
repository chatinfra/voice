package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chatinfra/voice/internal/cli"
)

func main() {
	if cli.WantsHelp(os.Args[1:]) {
		fmt.Fprint(os.Stdout, cli.VoicedHelp)
		return
	}
	logger := log.New(os.Stderr, "voiced: ", log.LstdFlags|log.LUTC)
	cfg, err := ConfigFromEnv()
	if err != nil {
		logger.Printf("configuration error: %v", err)
		os.Exit(1)
	}
	bridge, err := NewBridge(cfg, logger)
	if err != nil {
		logger.Printf("initialization error: %v", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := bridge.Run(ctx); err != nil {
		logger.Printf("fatal error: %v", err)
		os.Exit(1)
	}
}
