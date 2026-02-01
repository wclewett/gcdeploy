package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Session represents an SSH session for executing commands
type Session struct {
	client  *ssh.Client
	session *ssh.Session
}

// TerminalSession represents an interactive terminal session
type TerminalSession struct {
	client      *ssh.Client
	session     *ssh.Session
	stdinPipe   io.WriteCloser
	stdoutPipe  io.Reader
	stderrPipe  io.Reader
}

// StderrPipe returns the stderr pipe for reading error output
func (ts *TerminalSession) StderrPipe() io.Reader {
	return ts.stderrPipe
}

// NewClient creates a new SSH client connection
func NewClient(host, user string, authMethod ssh.AuthMethod) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // In production, use proper host key verification
	}

	client, err := ssh.Dial("tcp", host+":22", config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH: %w", err)
	}

	return client, nil
}

// NewSession creates a new SSH session from a client
func NewSession(client *ssh.Client) (*Session, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &Session{
		client:  client,
		session: session,
	}, nil
}

// Execute runs a command and returns the output
func (s *Session) Execute(command string) (string, error) {
	output, err := s.session.CombinedOutput(command)
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w", err)
	}
	return string(output), nil
}

// ExecuteWithOutput runs a command and returns stdout and stderr separately
func (s *Session) ExecuteWithOutput(command string) (stdout, stderr string, err error) {
	stdoutPipe, err := s.session.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := s.session.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := s.session.Start(command); err != nil {
		return "", "", fmt.Errorf("failed to start command: %w", err)
	}

	stdoutBytes, err := io.ReadAll(stdoutPipe)
	if err != nil {
		return "", "", err
	}

	stderrBytes, _ := io.ReadAll(stderrPipe)
	if err != nil {
		return "", "", err
	}

	err = s.session.Wait()
	if err != nil {
		return string(stdoutBytes), string(stderrBytes), fmt.Errorf("command failed: %w", err)
	}

	return string(stdoutBytes), string(stderrBytes), nil
}

// ExecuteStream executes a command and streams output chunks to a channel
// The channel will receive output chunks as they arrive, and will be closed when done
func (s *Session) ExecuteStream(command string, outputCh chan<- []byte) error {
	// Create a new session for this command
	newSession, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer newSession.Close()

	// Combine stderr with stdout
	newSession.Stderr = newSession.Stdout

	// Get stdout pipe (which now includes stderr)
	stdoutPipe, err := newSession.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Start the command
	if err := newSession.Start(command); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Stream output in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buffer)
			if n > 0 {
				// Send a copy of the buffer
				data := make([]byte, n)
				copy(data, buffer[:n])
				outputCh <- data
			}
			if err != nil {
				// EOF is expected when the stream ends
				return
			}
		}
	}()

	// Wait for command to complete
	err = newSession.Wait()
	// Wait for the reader goroutine to finish
	<-done
	close(outputCh)
	return err
}

// Close closes the SSH session and client
func (s *Session) Close() error {
	if s.session != nil {
		s.session.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// ErrPassphraseRequired is returned when a key requires a passphrase
var ErrPassphraseRequired = errors.New("passphrase required for SSH key")

// PublicKeyFile returns an ssh.AuthMethod from a private key file
// If passphrase is provided and the key is encrypted, it will be used
// If passphrase is empty and key is encrypted, returns ErrPassphraseRequired
func PublicKeyFile(file string, passphrase string) (ssh.AuthMethod, error) {
	buffer, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}

	// Try parsing without passphrase first
	key, err := ssh.ParsePrivateKey(buffer)
	if err == nil {
		// Key is not encrypted, return it
		return ssh.PublicKeys(key), nil
	}

	// Check if the error indicates the key is encrypted
	// ssh.ParsePrivateKey returns an error containing "passphrase" for encrypted keys
	if !strings.Contains(err.Error(), "passphrase") && !strings.Contains(err.Error(), "password") {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Key is encrypted, check if we have a passphrase
	if passphrase == "" {
		return nil, ErrPassphraseRequired
	}

	// Parse with passphrase
	key, err = ssh.ParsePrivateKeyWithPassphrase(buffer, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key with passphrase: %w", err)
	}

	return ssh.PublicKeys(key), nil
}

// NewTerminalSession creates a new interactive terminal session
func NewTerminalSession(client *ssh.Client) (*TerminalSession, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Request PTY for terminal emulation
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // Enable echoing
		ssh.TTY_OP_ISPEED: 14400, // Input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // Output speed = 14.4kbaud
	}

	// Request pseudo-terminal
	if err := session.RequestPty("xterm-256color", 80, 24, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to request PTY: %w", err)
	}

	// Get pipes for stdin, stdout, stderr
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start shell
	if err := session.Shell(); err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to start shell: %w", err)
	}

	return &TerminalSession{
		client:     client,
		session:    session,
		stdinPipe:  stdinPipe,
		stdoutPipe: stdoutPipe,
		stderrPipe: stderrPipe,
	}, nil
}

// Write sends data to the terminal stdin
func (ts *TerminalSession) Write(data []byte) error {
	if ts.stdinPipe == nil {
		return fmt.Errorf("stdin pipe is nil")
	}
	n, err := ts.stdinPipe.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write to stdin: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("partial write: wrote %d of %d bytes", n, len(data))
	}
	return nil
}

// Read reads data from terminal stdout
func (ts *TerminalSession) Read(p []byte) (n int, err error) {
	return ts.stdoutPipe.Read(p)
}

// Close closes the terminal session
func (ts *TerminalSession) Close() error {
	if ts.stdinPipe != nil {
		ts.stdinPipe.Close()
	}
	if ts.session != nil {
		ts.session.Close()
	}
	return nil
}

// Resize resizes the terminal
func (ts *TerminalSession) Resize(width, height int) error {
	return ts.session.WindowChange(height, width)
}

// DefaultPrivateKeyPath returns the default SSH private key path
func DefaultPrivateKeyPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "~/.ssh/google_compute_engine"
	}
	return filepath.Join(homeDir, ".ssh", "google_compute_engine")
}
