package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/wclewett/gcdeploy/internal/deploy"
)

const cfg_file = ".gcd.toml"

// DeploymentStep represents a single step in the deployment script
type DeploymentStep struct {
	Command string `toml:"command"`
	Target  string `toml:"target"` // "local" or "remote"
}

// Config represents the configuration from .gcd.toml
type Config struct {
	Instance        deploy.Instance   `toml:"instance"`
	Command         string            `toml:"command"`          // Optional if deployment is provided
	Deployment      []DeploymentStep  `toml:"deployment"`      // Optional deployment script
	CredentialsPath string            `toml:"credentials_path"` // Optional: path to GCP service account key file
	SSHKeyPath      string            `toml:"ssh_key_path"`     // Optional: path to SSH private key file
}

// Load reads and parses the .gcd.toml file from the current directory or parent directories
func Load() (*Config, error) {
	// Start from current directory and walk up to find .gcd.toml
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	var configPath string
	for {
		path := filepath.Join(dir, cfg_file)
		if _, err := os.Stat(path); err == nil {
			configPath = path
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			return nil, fmt.Errorf("%s not found in current directory or parent directories", cfg_file)
		}
		dir = parent
	}

	var config Config
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	// Validate required fields
	if config.Instance.Name == "" {
		return nil, fmt.Errorf("instance.name is required in %s", cfg_file)
	}
	if config.Instance.ProjectId == "" {
		return nil, fmt.Errorf("instance.project_id is required in %s", cfg_file)
	}
	if config.Instance.Zone == "" {
		return nil, fmt.Errorf("instance.zone is required in %s", cfg_file)
	}
	
	// Command is required if no deployment script is provided
	if config.Command == "" && len(config.Deployment) == 0 {
		return nil, fmt.Errorf("either command or deployment is required in %s", cfg_file)
	}
	
	// Validate deployment steps if provided
	for i, step := range config.Deployment {
		if step.Command == "" {
			return nil, fmt.Errorf("deployment[%d].command is required in %s", i, cfg_file)
		}
		if step.Target != "local" && step.Target != "remote" {
			return nil, fmt.Errorf("deployment[%d].target must be 'local' or 'remote' in %s", i, cfg_file)
		}
	}

	return &config, nil
}
