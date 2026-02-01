package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
)

// Instance represents a GCP VM instance
type Instance struct {
	Name      string `toml:"name"`
	ProjectId string `toml:"project_id"`
	Zone      string `toml:"zone"`
}

// InstanceDetails contains information about a VM instance needed for SSH connection
type InstanceDetails struct {
	Name       string
	ExternalIP string
	InternalIP string
	Username   string
	Status     string
}

// gcloudInstanceJSON represents the JSON structure returned by gcloud compute instances describe
type gcloudInstanceJSON struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	NetworkInterfaces []struct {
		NetworkIP string `json:"networkIP"`
		AccessConfigs []struct {
			NatIP string `json:"natIP"`
		} `json:"accessConfigs"`
	} `json:"networkInterfaces"`
}

// GetInstanceDetails retrieves VM instance information including external IP using gcloud CLI
func GetInstanceDetails(ctx context.Context, instance Instance, credentialsPath string) (*InstanceDetails, error) {
	// Use gcloud compute instances describe to get instance details
	// This uses the user's existing gcloud authentication, avoiding the need for OAuth keys
	cmd := exec.CommandContext(ctx,
		"gcloud",
		"compute",
		"instances",
		"describe",
		instance.Name,
		"--zone", instance.Zone,
		"--project", instance.ProjectId,
		"--format", "json",
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gcloud command failed: %s: %w", string(exitErr.Stderr), err)
		}
		return nil, fmt.Errorf("failed to run gcloud command: %w", err)
	}

	var vmInstance gcloudInstanceJSON
	if err := json.Unmarshal(output, &vmInstance); err != nil {
		return nil, fmt.Errorf("failed to parse gcloud output: %w", err)
	}

	details := &InstanceDetails{
		Name:     vmInstance.Name,
		Status:   vmInstance.Status,
		Username: getDefaultUsername(),
	}

	// Extract IP addresses from network interfaces
	for _, networkInterface := range vmInstance.NetworkInterfaces {
		// Get internal IP
		if details.InternalIP == "" {
			details.InternalIP = networkInterface.NetworkIP
		}

		// Get external IP from access configs
		for _, accessConfig := range networkInterface.AccessConfigs {
			if accessConfig.NatIP != "" {
				details.ExternalIP = accessConfig.NatIP
				break
			}
		}
		if details.ExternalIP != "" {
			break
		}
	}

	if details.ExternalIP == "" {
		return nil, fmt.Errorf("instance %s does not have an external IP address", instance.Name)
	}

	return details, nil
}

// VMConnect establishes an SSH connection to a GCP VM instance
func VMConnect(ctx context.Context, instance Instance) (*Session, error) {
	return VMConnectWithKey(ctx, instance, "", "", "")
}

// VMConnectWithKey establishes an SSH connection to a GCP VM instance using a specific SSH key
// If passphrase is empty and key is encrypted, returns ErrPassphraseRequired wrapped in error
func VMConnectWithKey(ctx context.Context, instance Instance, sshKeyPath string, credentialsPath string, passphrase string) (*Session, error) {
	// Get instance details
	details, err := GetInstanceDetails(ctx, instance, credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance details: %w", err)
	}

	// Determine SSH key path
	if sshKeyPath == "" {
		sshKeyPath = DefaultPrivateKeyPath()
	}

	// Load SSH private key
	authMethod, err := PublicKeyFile(sshKeyPath, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH key: %w", err)
	}

	// Create SSH client
	client, err := NewClient(details.ExternalIP, details.Username, authMethod)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	// Create SSH session
	session, err := NewSession(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}

	return session, nil
}

// VMConnectTerminal establishes an interactive terminal session to a GCP VM instance
func VMConnectTerminal(ctx context.Context, instance Instance, sshKeyPath string, credentialsPath string, passphrase string) (*TerminalSession, error) {
	// Get instance details
	details, err := GetInstanceDetails(ctx, instance, credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance details: %w", err)
	}

	// Determine SSH key path
	if sshKeyPath == "" {
		sshKeyPath = DefaultPrivateKeyPath()
	}

	// Load SSH private key
	authMethod, err := PublicKeyFile(sshKeyPath, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH key: %w", err)
	}

	// Create SSH client
	client, err := NewClient(details.ExternalIP, details.Username, authMethod)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	// Create terminal session
	termSession, err := NewTerminalSession(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create terminal session: %w", err)
	}

	return termSession, nil
}

// getDefaultUsername returns the default username for SSH connection
// This can be overridden by checking GCP metadata or OS Login
func getDefaultUsername() string {
	// Try to get current user
	currentUser, err := user.Current()
	if err == nil && currentUser != nil {
		return currentUser.Username
	}

	// Fallback to common Linux usernames
	// In production, you might want to check GCP metadata or OS Login
	if username := os.Getenv("USER"); username != "" {
		return username
	}

	// Default fallback
	return "user"
}
