# Example Deployment Configuration

This directory contains example files demonstrating how to use `gcdeploy` for VM deployments.

## Files

- **`deploy.sh`**: Original bash deployment script that demonstrates a complete deployment workflow
- **`.gcd.toml`**: TOML configuration file that converts the bash script into a `gcdeploy`-compatible format

## Converting a Bash Script to .gcd.toml

The `.gcd.toml` file shows how to convert a bash deployment script into a structured configuration:

1. **Instance Configuration**: Define your GCP VM instance details
   ```toml
   [instance]
   name = "your-vm-name"
   project_id = "your-project-id"
   zone = "us-central1-c"
   ```

2. **Deployment Steps**: Convert each function/step into a deployment step
   ```toml
   [[deployment]]
   target = "remote"  # or "local"
   command = "your command here"
   ```

3. **Key Differences**:
   - **Sequential Execution**: Steps run one after another automatically
   - **Local vs Remote**: Use `target = "local"` for commands that run on your machine, `target = "remote"` for VM commands
   - **Code Transfer**: Split file transfer operations into:
     - Local step: Create archive and transfer (using `gcloud compute scp`)
     - Remote step: Extract archive on the VM

## Example Workflow

The example `.gcd.toml` demonstrates:

1. **Install Dependencies** (remote): Go, Node.js, templ CLI
2. **Deploy Code** (local + remote): Create archive locally, transfer, extract on VM
3. **Install Dependencies** (remote): Go modules, npm packages
4. **Build Application** (remote): Compile the application
5. **Setup Service** (remote): Create systemd service file
6. **Start Service** (remote): Enable and start the service
7. **Verify Deployment** (remote): Check service status and logs

## Customization

To use this example for your project:

1. Copy `.gcd.toml` to your project root
2. Update the `[instance]` section with your VM details
3. Modify deployment steps to match your:
   - Application directory (`APP_DIR`)
   - Application user (`APP_USER`)
   - Service name (`SERVICE_NAME`)
   - Build commands
   - File exclusions in the tar command

## Notes

- **Variable Expansion**: Shell variables like `$USER` are expanded by the shell when commands run
- **Complex Commands**: For multi-line commands (like systemd service files), use `echo -e` with `\n` escape sequences or consider transferring files separately
- **Error Handling**: The deployment stops if any step fails (commands should use proper error handling)
- **Interactive Prompts**: Avoid commands that require interactive input; use environment variables or configuration files instead
