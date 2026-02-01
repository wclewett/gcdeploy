package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wclewett/gcdeploy/internal/config"
	"github.com/wclewett/gcdeploy/internal/tui"
)

func main() {
	// Parse command line flags
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Load configuration from .gcd.toml
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	model, err := tui.New(*debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not initialize Bubble Tea model: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Set up the model with instance, command, and deployment steps from config
	model.SetInstanceAndCommand(ctx, cfg.Instance, cfg.Command, cfg.CredentialsPath, cfg.SSHKeyPath, cfg.Deployment)

	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
