# GCDEPLOY

A terminal-based deployment tool for Google Cloud Platform (GCP) Compute Engine VMs. GCDEPLOY provides an interactive TUI (Terminal User Interface) for managing deployments, executing commands, and maintaining SSH sessions with your GCP virtual machines.

## Features

- **Interactive Terminal UI**: Beautiful, responsive terminal interface built with Bubble Tea
- **Hybrid Deployment System**: Automate deployments with scripts that can run both locally and remotely
- **Dual Shell Mode**: Seamlessly switch between local and remote shells while preserving SSH sessions
- **GCP Integration**: Uses `gcloud` CLI for authentication and VM instance management
- **Passphrase Support**: Secure handling of passphrase-protected SSH keys
- **Real-time Output**: Stream command output in real-time with proper formatting

## Prerequisites

- Go 1.24+ installed
- `gcloud` CLI installed and authenticated (`gcloud auth login`)
- Access to GCP Compute Engine instances
- SSH key pair for connecting to your VMs (optional, can use default GCP keys)

## Installation

```bash
git clone https://github.com/wclewett/gcdeploy.git
cd gcdeploy
go build -o gcdeploy
```

Or install directly:

```bash
go install github.com/wclewett/gcdeploy@latest
```

## Configuration

GCDEPLOY uses a `.gcd.toml` configuration file to define your deployment targets and scripts. The configuration file is searched in the current directory and parent directories.

### Basic Configuration

Create a `.gcd.toml` file in your project root:

```toml
[instance]
name = "my-vm-instance"
project_id = "my-gcp-project"
zone = "us-central1-a"

command = "echo 'Hello from VM'"
```

### Configuration Fields

#### Required Fields

- **`instance.name`**: The name of your GCP Compute Engine VM instance
- **`instance.project_id`**: Your GCP project ID
- **`instance.zone`**: The GCP zone where your VM is located

#### Optional Fields

- **`command`**: A single command to execute on the VM (required if `deployment` is not provided)
- **`deployment`**: An array of deployment steps (required if `command` is not provided)
- **`credentials_path`**: Path to GCP service account key file (optional, uses `gcloud` auth by default)
- **`ssh_key_path`**: Path to your SSH private key file (optional, uses default GCP keys)

### Deployment Scripts

For more complex deployments, you can define a multi-step deployment script that runs commands both locally and remotely:

```toml
[instance]
name = "my-vm-instance"
project_id = "my-gcp-project"
zone = "us-central1-a"

[[deployment]]
command = "git pull origin main"
target = "local"

[[deployment]]
command = "docker build -t myapp:latest ."
target = "local"

[[deployment]]
command = "docker save myapp:latest | docker load"
target = "remote"

[[deployment]]
command = "docker-compose up -d"
target = "remote"
```

Each deployment step has:
- **`command`**: The command to execute
- **`target`**: Either `"local"` (runs on your machine) or `"remote"` (runs on the VM)

### Example Configurations

#### Simple Command Execution

```toml
[instance]
name = "web-server"
project_id = "my-project"
zone = "us-west1-a"

command = "sudo systemctl status nginx"
```

#### Deployment with Custom SSH Key

```toml
[instance]
name = "production-server"
project_id = "my-project"
zone = "us-east1-b"

ssh_key_path = "~/.ssh/gcp_key"
credentials_path = "~/.config/gcp/service-account.json"

[[deployment]]
command = "npm run build"
target = "local"

[[deployment]]
command = "rsync -avz dist/ user@production-server:/var/www/app/"
target = "local"

[[deployment]]
command = "cd /var/www/app && npm install --production"
target = "remote"

[[deployment]]
command = "pm2 restart app"
target = "remote"
```

## How It Works

### Architecture

GCDEPLOY uses a hybrid approach combining automated deployment scripts with interactive terminal access:

1. **Configuration Loading**: Reads `.gcd.toml` from the current directory or parent directories
2. **VM Connection**: Uses `gcloud` CLI to retrieve VM instance details (IP addresses, status)
3. **SSH Authentication**: Establishes SSH connection using provided or default SSH keys
4. **Deployment Execution**: If a deployment script is defined, executes steps sequentially
5. **Interactive Terminal**: After deployment (or immediately if no script), provides an interactive terminal
6. **Shell Mode Switching**: Allows toggling between local and remote shells with `Shift+Tab`

### Workflow

```
┌─────────────────┐
│  Load .gcd.toml │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Get VM Details  │
│  via gcloud CLI │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  SSH Connection │
└────────┬────────┘
         │
         ▼
┌─────────────────┐      ┌──────────────┐
│  Deployment?    │ Yes  │ Run Steps    │
│                 ├──────┤ Sequentially │
└────────┬────────┘      └──────┬───────┘
         │ No                   │
         │                      │
         └──────────┬───────────┘
                    │
                    ▼
         ┌──────────────────┐
         │ Interactive TUI  │
         │  Terminal Mode   │
         └──────────────────┘
```

### Key Components

- **TUI Model**: Manages the terminal interface state, input/output, and rendering
- **SSH Session**: Handles SSH connections, PTY management, and command execution
- **Deployment Engine**: Orchestrates multi-step deployments with local/remote execution
- **GCP Integration**: Uses `gcloud` CLI for VM metadata retrieval

## Usage

### Basic Usage

```bash
# Run with default configuration
gcdeploy

# Enable debug logging
gcdeploy -debug
```

### Interactive Commands

Once in the TUI:

- **Type commands**: Enter commands directly in the prompt
- **Shift+Tab**: Toggle between local shell (orange prompt) and remote shell (blue prompt)
- **Ctrl+C**: Send interrupt signal to the current shell
- **q**: Quit the application
- **↑/↓**: Navigate command history (in terminal mode)

### Shell Modes

- **Remote Shell Mode** (default): Commands execute on the connected GCP VM
  - Prompt color: Blue (Go gopher blue)
  - Shows: `$ ` prompt
  - Terminal output shows: `user@vm-name $ ` from the remote shell

- **Local Shell Mode**: Commands execute on your local machine
  - Prompt color: Orange (Rust crab orange)
  - Shows: `$ ` prompt
  - Terminal output shows: `user@hostname $ ` from your local shell

### Status Messages

The TUI displays status messages below the command prompt:
- **`[INFO]`**: Informational messages (gray)
- **`[SUCCESS]`**: Success messages (green)
- **`[ERROR]`**: Error messages (red)

## Authentication

GCDEPLOY uses `gcloud` CLI for GCP authentication. Before using the tool:

1. Install `gcloud` CLI: https://cloud.google.com/sdk/docs/install
2. Authenticate: `gcloud auth login`
3. Set your project: `gcloud config set project YOUR_PROJECT_ID`

For SSH access, GCDEPLOY will:
1. First try to use the SSH key specified in `ssh_key_path`
2. Fall back to default GCP SSH keys if not specified
3. Prompt for passphrase if the key is protected

## Troubleshooting

### "gcloud crashed (EOFError)"
Make sure you've authenticated with `gcloud auth login` before running GCDEPLOY.

### "instance.name is required"
Ensure your `.gcd.toml` file has the `[instance]` section with all required fields.

### "SSH key requires a passphrase"
If your SSH key is passphrase-protected, GCDEPLOY will prompt you to enter it in the TUI.

### Terminal not responding
Try pressing `Ctrl+C` to send an interrupt signal, or quit with `q` and restart.

## License

[Add your license here]

## Contributing

[Add contribution guidelines here]
