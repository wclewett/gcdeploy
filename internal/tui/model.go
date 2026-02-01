package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/wclewett/gcdeploy/internal/config"
	"github.com/wclewett/gcdeploy/internal/deploy"
)

var helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render

// Go gopher blue color (#00ADD8)
const gopherBlue = "#00ADD8"
// Rust crab orange color (#CE412B)
const rustCrab = "#CE412B"
const gutter = 2

// ShellMode represents the current shell mode
type ShellMode int

const (
	RemoteShell ShellMode = iota
	LocalShell
)

// VimMode represents the vim editing mode
type VimMode int

const (
	InsertMode VimMode = iota
	NormalMode
)

// SSHOutputMsg is sent when new SSH output arrives
type SSHOutputMsg struct {
	Data []byte
}

// SSHErrorMsg is sent when an SSH error occurs
type SSHErrorMsg struct {
	Error error
}

// PassphraseNeededMsg is sent when SSH key requires a passphrase
type PassphraseNeededMsg struct {
	KeyPath string
}

// PassphraseSubmittedMsg is sent when user submits passphrase
type PassphraseSubmittedMsg struct {
	Passphrase string
}

// TerminalConnectedMsg is sent when terminal session is established
type TerminalConnectedMsg struct {
	Session *deploy.TerminalSession
}

// DeploymentStepMsg is sent when a deployment step starts
type DeploymentStepMsg struct {
	StepNum int
	Total   int
	Step    config.DeploymentStep
}

// DeploymentCompleteMsg is sent when deployment script completes
type DeploymentCompleteMsg struct{}

// tickMsg is sent periodically to check for new output
type tickMsg struct{}

type Model struct {
	viewport        viewport.Model
	content         string
	width           int
	height          int
	outputCh        chan []byte
	errCh           chan error
	session         *deploy.Session
	terminalSession *deploy.TerminalSession
	instance        deploy.Instance
	command         string
	credentialsPath string
	sshKeyPath      string
	ctx             context.Context
	
	// Passphrase input state
	passphraseInput textinput.Model
	needsPassphrase bool
	pendingPassphrase string
	
	// Terminal mode
	terminalMode bool
	terminalInputCh chan []byte
	terminalOutputCh chan []byte
	commandInput textinput.Model
	commandHistory []string
	
	// Shell mode (local vs remote)
	shellMode ShellMode
	
	// Vim mode (insert vs normal)
	vimMode VimMode
	
	// Deployment script state
	deploymentSteps []config.DeploymentStep
	currentStep int
	deploymentRunning bool
	deploymentComplete bool
	
	// Local shell output
	localOutputCh chan []byte
	localErrCh chan error
	
	// TUI status messages (shown below command prompt)
	statusMessage string
	
	// Shell prompt info
	localUser    string
	localHost    string
	remoteUser   string
	remoteHost   string
	
	// Debug mode
	debug bool
}

func New(debug bool) (*Model, error) {
	vp := viewport.New(0, 0)

	// Create border style with Go gopher blue
	borderStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(gopherBlue)).
		Padding(gutter, gutter, gutter, gutter)

	vp.Style = borderStyle

	// Initialize passphrase input
	passphraseTi := textinput.New()
	passphraseTi.Placeholder = "Enter passphrase"
	passphraseTi.EchoMode = textinput.EchoPassword
	passphraseTi.CharLimit = 256
	passphraseTi.Width = 50

	// Initialize command input for terminal mode
	commandTi := textinput.New()
	commandTi.Placeholder = "Enter command..."
	commandTi.CharLimit = 1000
	commandTi.Width = 80

	// Get local user and hostname
	localUser, localHost := getLocalUserHost()
	
	return &Model{
		viewport:         vp,
		content:          "",
		passphraseInput:  passphraseTi,
		commandInput:     commandTi,
		needsPassphrase:  false,
		terminalMode:     false,
		terminalInputCh:  make(chan []byte, 100),
		terminalOutputCh: make(chan []byte, 100),
		commandHistory:   make([]string, 0),
		shellMode:        RemoteShell, // Start in remote mode
		vimMode:          InsertMode,   // Start in insert mode
		deploymentSteps:  nil,
		currentStep:      0,
		deploymentRunning: false,
		deploymentComplete: false,
		localOutputCh:    make(chan []byte, 100),
		localErrCh:       make(chan error, 1),
		statusMessage:    "",
		localUser:        localUser,
		localHost:        localHost,
		remoteUser:       "user", // Default, will be updated when SSH connects
		remoteHost:       "remote",
		debug:            debug,
	}, nil
}

// getLocalUserHost returns the local username and hostname
func getLocalUserHost() (string, string) {
	// Get username
	username := "user"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	
	// Get hostname
	hostname := "localhost"
	if h, err := os.Hostname(); err == nil {
		hostname = h
	}
	
	return username, hostname
}

func (m Model) Init() tea.Cmd {
	// If deployment script exists, start with that
	// Otherwise, start with SSH connection
	if len(m.deploymentSteps) > 0 {
		// First establish SSH connection, then run deployment
		return tea.Batch(
			tea.EnterAltScreen,
			m.StartTerminalSession(m.ctx, m.instance, m.command, m.credentialsPath, m.sshKeyPath, ""),
			tick(),
		)
	} else {
		// No deployment script, use regular command execution
		if !strings.HasPrefix(m.content, "$ ") {
			m.content = m.buildContentHeader()
		}
		return tea.Batch(
			tea.EnterAltScreen,
			m.StartSSHStream(m.ctx, m.instance, m.command, m.credentialsPath, m.sshKeyPath),
			tick(),
		)
	}
}

// SetInstanceAndCommand sets the instance and command for SSH streaming
func (m *Model) SetInstanceAndCommand(
	ctx context.Context,
	instance deploy.Instance,
	command string,
	credentialsPath string,
	sshKeyPath string,
	deploymentSteps []config.DeploymentStep,
) {
	m.ctx = ctx
	m.instance = instance
	m.command = command
	m.credentialsPath = credentialsPath
	m.sshKeyPath = sshKeyPath
	m.deploymentSteps = deploymentSteps

		// Initialize content
		if len(deploymentSteps) > 0 {
			m.statusMessage = "[INFO] Deployment script detected. Starting deployment..."
			m.content = ""
		} else {
		// Initialize content with the command displayed at the top
		// Use a default width for separator, will be updated on window resize
		m.content = fmt.Sprintf("$ %s\n", command)
		m.content += strings.Repeat("─", 80) + "\n\n"
	}
}

// buildContentHeader builds the command header with separator
func (m Model) buildContentHeader() string {
	width := m.viewport.Width
	if width <= 0 {
		// Calculate from window width if viewport width not set yet
		if m.width > 0 {
			borderWidth := 2 + (gutter * 2)
			width = m.width - borderWidth
		}
		if width <= 0 {
			width = 80 // Final fallback
		}
	}
	header := fmt.Sprintf("$ %s\n", m.command)
	separator := strings.Repeat("─", width) + "\n\n"
	return header + separator
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle passphrase input
		if m.needsPassphrase {
			switch msg.String() {
			case "enter":
				// Submit passphrase
				m.pendingPassphrase = m.passphraseInput.Value()
				if m.pendingPassphrase == "" {
					// Empty passphrase, don't proceed
					return m, nil
				}
				m.needsPassphrase = false
				m.passphraseInput.SetValue("")
				m.passphraseInput.Blur()
				// Show connecting message in status
				m.statusMessage = "[INFO] Passphrase received. Connecting..."
				m.viewport.SetContent(m.wrapContent(m.content))
				m.viewport.GotoBottom()
				// Retry connection with passphrase - use terminal mode
				return m, tea.Batch(
					m.StartTerminalSession(m.ctx, m.instance, m.command, m.credentialsPath, m.sshKeyPath, m.pendingPassphrase),
					tick(),
				)
			case "esc":
				// Cancel passphrase input
				m.needsPassphrase = false
				m.passphraseInput.SetValue("")
				m.passphraseInput.Blur()
				m.statusMessage = "[INFO] Passphrase input cancelled"
				m.viewport.SetContent(m.wrapContent(m.content))
				m.viewport.GotoBottom()
				return m, nil
			default:
				// Update passphrase input
				m.passphraseInput, _ = m.passphraseInput.Update(msg)
				return m, nil
			}
		}
		
		
		// Handle terminal mode input with command input field
		if m.terminalMode {
			keyStr := msg.String()
			
			// Handle vim mode toggle (Escape key)
			if keyStr == "esc" {
				if m.vimMode == InsertMode {
					m.vimMode = NormalMode
					m.statusMessage = "[INFO] Normal mode (press 'i' to insert, 'q' to quit)"
					m.commandInput.Blur()
				} else {
					m.vimMode = InsertMode
					m.statusMessage = "[INFO] Insert mode"
					// Focus command input when entering insert mode
					m.commandInput.Focus()
				}
				// Clear status message after a short delay
				return m, tea.Sequence(
					tick(),
					tea.Tick(2*time.Second, func(time.Time) tea.Msg {
						return tickMsg{}
					}),
				)
			}
			
			// Handle quit only in normal mode
			if keyStr == "q" && m.vimMode == NormalMode {
				if m.terminalSession != nil {
					m.terminalSession.Close()
				}
				if m.session != nil {
					m.session.Close()
				}
				return m, tea.Quit
			}
			
			// Handle 'i' key in normal mode to enter insert mode
			if keyStr == "i" && m.vimMode == NormalMode {
				m.vimMode = InsertMode
				m.statusMessage = "[INFO] Insert mode"
				m.commandInput.Focus()
				// Clear status message after a short delay
				return m, tea.Sequence(
					tick(),
					tea.Tick(2*time.Second, func(time.Time) tea.Msg {
						return tickMsg{}
					}),
				)
			}
			
			// In normal mode, only allow special keys (quit, insert, mode toggle)
			// All other keys are ignored
			if m.vimMode == NormalMode {
				// Allow Shift+Tab for shell mode switching
				if keyStr == "shift+tab" {
					// Fall through to handle mode toggle below
				} else {
					// Ignore all other keys in normal mode
					return m, nil
				}
			}
			
			// Handle mode toggle (Shift+Tab)
			if keyStr == "shift+tab" {
				// Toggle between local and remote shell
				if m.shellMode == RemoteShell {
					m.shellMode = LocalShell
					m.statusMessage = "[INFO] Switched to local shell mode"
				} else {
					m.shellMode = RemoteShell
					m.statusMessage = "[INFO] Switched to remote shell mode"
				}
				// Clear command input when switching modes
				m.commandInput.SetValue("")
				// Ensure command input is focused and updated
				m.commandInput.Focus()
				var inputCmd tea.Cmd
				m.commandInput, inputCmd = m.commandInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{}})
				return m, inputCmd
			}
			
			// Handle command input (only process in insert mode)
			if m.vimMode != InsertMode {
				// In normal mode, we've already handled special keys above
				return m, nil
			}
			
			switch keyStr {
			case "enter":
				// Execute command
				commandText := m.commandInput.Value()
				// Clear input immediately before sending
				m.commandInput.SetValue("")
				var inputCmd tea.Cmd
				m.commandInput, inputCmd = m.commandInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{}})
				
				if commandText != "" {
					// Add to history
					m.commandHistory = append(m.commandHistory, commandText)
					
					// Clear status message when executing command
					m.statusMessage = ""
					
					// Route command based on shell mode
					if m.shellMode == LocalShell {
						// Execute locally
						return m, tea.Batch(
							m.StartLocalCommand(commandText),
							tick(),
							inputCmd,
						)
					} else {
						// Execute remotely
						if m.terminalSession == nil {
							// Send error to output channel
							select {
							case m.terminalOutputCh <- []byte(fmt.Sprintf("\n[ERROR] Terminal session is nil\n")):
							default:
							}
						} else {
							// Send command to terminal (terminal will echo it)
							commandWithNewline := commandText + "\n"
							if err := m.terminalSession.Write([]byte(commandWithNewline)); err != nil {
								// Send error to output channel so it displays
								select {
								case m.terminalOutputCh <- []byte(fmt.Sprintf("\n[ERROR] Failed to send command: %v\n", err)):
								default:
								}
							} else if m.debug {
								// Debug: confirm command was sent
								select {
								case m.terminalOutputCh <- []byte(fmt.Sprintf("\n[DEBUG] Sent command: %s\n", commandText)):
								default:
								}
							}
						}
					}
				}
				return m, tea.Batch(tick(), inputCmd)
			case "ctrl+c":
				// Send interrupt to terminal
				if m.terminalSession != nil {
					m.terminalSession.Write([]byte{3})
				}
				// Clear command input
				m.commandInput.SetValue("")
				var inputCmd tea.Cmd
				m.commandInput, inputCmd = m.commandInput.Update(msg)
				return m, tea.Batch(tick(), inputCmd)
			case "up":
				// Navigate command history
				if len(m.commandHistory) > 0 {
					m.commandInput.SetValue(m.commandHistory[len(m.commandHistory)-1])
				}
				var inputCmd tea.Cmd
				m.commandInput, inputCmd = m.commandInput.Update(msg)
				return m, inputCmd
			default:
				// Update command input for regular typing
				var inputCmd tea.Cmd
				m.commandInput, inputCmd = m.commandInput.Update(msg)
				return m, inputCmd
			}
		}
		
		switch msg.String() {
		case "ctrl+c", "q":
			if m.session != nil {
				m.session.Close()
			}
			return m, tea.Quit
		case "up":
			if !m.needsPassphrase && !m.terminalMode {
				m.viewport.ScrollUp(1)
			}
		case "down":
			if !m.needsPassphrase && !m.terminalMode {
				m.viewport.ScrollDown(1)
			}
		case "ctrl+u":
			if !m.needsPassphrase && !m.terminalMode {
				m.viewport.PageUp()
			}
		case "ctrl+d":
			if !m.needsPassphrase && !m.terminalMode {
				m.viewport.PageDown()
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// The border style handles padding, so we need to account for:
		// - Border lines (top + bottom = 2)
		// - Padding (gutter * 2 for top + bottom)
		// - Help text (1 line)
		borderStyle := m.viewport.Style
		borderHeight := borderStyle.GetVerticalFrameSize()
		helpHeight := 1
		availableHeight := msg.Height - borderHeight - helpHeight
		if availableHeight < 1 {
			availableHeight = 1
		}

		// Width: border handles padding
		borderWidth := borderStyle.GetHorizontalFrameSize()
		availableWidth := msg.Width - borderWidth
		if availableWidth < 1 {
			availableWidth = 1
		}

		m.viewport.Width = availableWidth
		m.viewport.Height = availableHeight

		// Update command input width in terminal mode
		if m.terminalMode {
			m.commandInput.Width = availableWidth - 2 // Account for "$ " prompt (2 chars)
			if m.commandInput.Width < 1 {
				m.commandInput.Width = 1
			}
			
			// Resize terminal PTY to match viewport width
			// This ensures terminal output respects the border
			if m.terminalSession != nil && availableWidth > 0 && availableHeight > 0 {
				m.terminalSession.Resize(availableWidth, availableHeight)
			}
		}

		// Ensure content has command header (if not in terminal mode)
		if !m.terminalMode && !strings.HasPrefix(m.content, "$ ") {
			m.content = m.buildContentHeader() + m.content
		}
		
		// For terminal mode, wrap content to viewport width to respect border
		// We need to wrap even terminal content to prevent overflow
		if m.terminalMode {
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
		} else {
			m.viewport.SetContent(m.wrapContent(m.content))
		}

	case tickMsg:
		// Handle terminal mode output
		if m.terminalMode {
			// Read output from both local and remote channels
			outputReceived := false
			maxReads := 10 // Limit reads per tick to avoid blocking
			reads := 0
			
			// Read from remote terminal output
			for reads < maxReads {
				select {
				case data, ok := <-m.terminalOutputCh:
					if ok {
						m.content += string(data)
						outputReceived = true
						reads++
						// Clear informational status messages when output arrives
						// But preserve vim mode status messages
						if m.statusMessage != "" && 
						   (strings.HasPrefix(m.statusMessage, "[INFO]") || 
						    strings.HasPrefix(m.statusMessage, "[SUCCESS]")) &&
						   !strings.Contains(m.statusMessage, "mode") {
							m.statusMessage = ""
						}
					} else {
						goto doneRemoteReading
					}
				default:
					goto doneRemoteReading
				}
			}
		doneRemoteReading:
			
			// Read from local command output
			for reads < maxReads {
				select {
				case data, ok := <-m.localOutputCh:
					if ok {
						m.content += string(data)
						outputReceived = true
						reads++
						// Clear informational status messages when output arrives
						// But preserve vim mode status messages
						if m.statusMessage != "" && 
						   (strings.HasPrefix(m.statusMessage, "[INFO]") || 
						    strings.HasPrefix(m.statusMessage, "[SUCCESS]")) &&
						   !strings.Contains(m.statusMessage, "mode") {
							m.statusMessage = ""
						}
					} else {
						goto doneLocalReading
					}
				default:
					goto doneLocalReading
				}
			}
		doneLocalReading:
			
			// Check for local errors
			select {
			case err := <-m.localErrCh:
				m.content += fmt.Sprintf("\n[ERROR] %v\n", err)
				outputReceived = true
			default:
			}
			
			if outputReceived {
				// Wrap terminal content to respect viewport width and border
				m.viewport.SetContent(m.wrapTerminalContent(m.content))
				m.viewport.GotoBottom()
			}
		} else {
			// Check for new output from channels (command execution mode)
			select {
			case data, ok := <-m.outputCh:
				if ok {
					m.content += string(data)
					m.viewport.SetContent(m.wrapContent(m.content))
					m.viewport.GotoBottom()
				}
			case err := <-m.errCh:
				m.content += fmt.Sprintf("\n[ERROR] %v\n", err)
				m.viewport.SetContent(m.wrapContent(m.content))
				m.viewport.GotoBottom()
			default:
				// No new data
			}
		}
		return m, tick()
	
	case LocalOutputMsg:
		// Append local command output
		m.content += string(msg.Data)
		// Clear status message when output arrives
		if m.statusMessage != "" && !strings.HasPrefix(m.statusMessage, "[") {
			m.statusMessage = ""
		}
		if m.terminalMode {
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
		} else {
			m.viewport.SetContent(m.wrapContent(m.content))
		}
		m.viewport.GotoBottom()
		
		// If deployment is running and this is a local step, check if we should continue
		// (We'll continue after a delay to allow output to finish)
		if m.deploymentRunning {
			return m, tea.Sequence(
				tick(),
				tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
					return tickMsg{}
				}),
				m.ContinueDeployment(),
			)
		}
		return m, tick()
	
	case LocalErrorMsg:
		// Handle local command error
		m.statusMessage = fmt.Sprintf("[ERROR] Local command failed: %v", msg.Error)
		if m.terminalMode {
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
		} else {
			m.viewport.SetContent(m.wrapContent(m.content))
		}
		m.viewport.GotoBottom()
		// If deployment is running, stop it
		if m.deploymentRunning {
			m.deploymentRunning = false
			m.statusMessage = "[INFO] Deployment stopped due to error"
		}
		return m, tick()
	
	case DeploymentStepMsg:
		// If this is a trigger message (StepNum == 0 and empty step), start deployment
		if msg.StepNum == 0 && msg.Step.Command == "" && !m.deploymentRunning {
			return m, m.StartDeploymentScript()
		}
		
		// Show deployment step info in status
		targetLabel := "local"
		if msg.Step.Target == "remote" {
			targetLabel = "remote"
		}
		m.statusMessage = fmt.Sprintf("[%d/%d] Running %s: %s", msg.StepNum, msg.Total, targetLabel, msg.Step.Command)
		if m.terminalMode {
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
		} else {
			m.viewport.SetContent(m.wrapContent(m.content))
		}
		m.viewport.GotoBottom()
		
		// For remote steps, wait a bit then continue to next step
		// For local steps, wait for command completion (handled in LocalOutputMsg)
		if msg.Step.Target == "remote" {
			return m, tea.Sequence(
				tea.Tick(2*time.Second, func(time.Time) tea.Msg {
					return tickMsg{}
				}),
				m.ContinueDeployment(),
			)
		}
		return m, tick()
	
	case DeploymentCompleteMsg:
		// Deployment complete
		m.deploymentRunning = false
		m.deploymentComplete = true
		m.statusMessage = "[SUCCESS] Deployment script completed. SSH session preserved for manual use."
		if m.terminalMode {
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
		} else {
			m.viewport.SetContent(m.wrapContent(m.content))
		}
		m.viewport.GotoBottom()
		return m, tick()

	case SSHOutputMsg:
		// Append new output to content
		// Clear status message when output arrives
		if strings.Contains(m.statusMessage, "Connecting") {
			m.statusMessage = ""
		}
		m.content += string(msg.Data)
		m.viewport.SetContent(m.wrapContent(m.content))
		// Auto-scroll to bottom
		m.viewport.GotoBottom()
		return m, tick()
	
	case TerminalConnectedMsg:
		// Store the terminal session from the message
		m.terminalSession = msg.Session
		m.terminalMode = true
		
		// Clear connecting message and set status
		m.statusMessage = "[SUCCESS] Terminal connected. Waiting for shell..."
		
		// Update remote user/host from instance details
		// Try to get instance details to set remote hostname
		if details, err := deploy.GetInstanceDetails(m.ctx, m.instance, m.credentialsPath); err == nil {
			m.remoteHost = details.Name
			m.remoteUser = details.Username
		} else {
			// Fallback to instance name if we can't get details
			m.remoteHost = m.instance.Name
		}
		
		// Resize terminal to match viewport if we have dimensions
		// Use the available width (accounting for border padding) to ensure output respects border
		if m.terminalSession != nil {
			termWidth := m.viewport.Width
			termHeight := m.viewport.Height
			if termWidth <= 0 {
				// Fallback: calculate from window size if viewport not set yet
				if m.width > 0 {
					borderStyle := m.viewport.Style
					borderWidth := borderStyle.GetHorizontalFrameSize()
					termWidth = m.width - borderWidth
				}
				if termWidth <= 0 {
					termWidth = 80 // Final fallback
				}
			}
			if termHeight <= 0 {
				if m.height > 0 {
					borderStyle := m.viewport.Style
					borderHeight := borderStyle.GetVerticalFrameSize()
					termHeight = m.height - borderHeight - 1 // Account for help text
				}
				if termHeight <= 0 {
					termHeight = 24 // Final fallback
				}
			}
			m.terminalSession.Resize(termWidth, termHeight)
		}
		
		// Focus command input
		m.commandInput.Focus()
		
		// If deployment script exists, start it after a short delay
		// Otherwise, execute the initial command
		if len(m.deploymentSteps) > 0 {
			// Start deployment script after shell initializes
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
			m.viewport.GotoBottom()
			return m, tea.Batch(
				tick(),
				textinput.Blink,
				tea.Tick(1000*time.Millisecond, func(time.Time) tea.Msg {
					// Start deployment
					return DeploymentStepMsg{
						StepNum: 0,
						Total:   len(m.deploymentSteps),
						Step:    config.DeploymentStep{},
					}
				}),
			)
		} else {
			// Execute the initial command if provided
			if m.terminalSession != nil && m.command != "" {
				go func() {
					// Wait for shell to initialize
					time.Sleep(800 * time.Millisecond)
					
					commandWithNewline := m.command + "\n"
					if err := m.terminalSession.Write([]byte(commandWithNewline)); err != nil {
						select {
						case m.terminalOutputCh <- []byte(fmt.Sprintf("\n[ERROR] Failed to send command: %v\n", err)):
						default:
						}
					}
					// Don't echo command here - let the terminal handle it naturally
				}()
			}
			m.viewport.SetContent(m.wrapTerminalContent(m.content))
			m.viewport.GotoBottom()
			return m, tea.Batch(tick(), textinput.Blink)
		}
	

	case SSHErrorMsg:
		// Check if error is due to missing passphrase
		if errors.Is(msg.Error, deploy.ErrPassphraseRequired) || 
		   strings.Contains(msg.Error.Error(), "passphrase required") {
			if !m.needsPassphrase {
				m.needsPassphrase = true
				m.passphraseInput.Focus()
				m.statusMessage = "[INFO] SSH key requires a passphrase. Please enter it below."
				m.viewport.SetContent(m.wrapContent(m.content))
				m.viewport.GotoBottom()
				return m, textinput.Blink
			}
		} else {
			m.statusMessage = fmt.Sprintf("[ERROR] %v", msg.Error)
			m.viewport.SetContent(m.wrapContent(m.content))
			m.viewport.GotoBottom()
		}

	case PassphraseNeededMsg:
		m.needsPassphrase = true
		m.passphraseInput.Focus()
		m.statusMessage = fmt.Sprintf("[INFO] SSH key at %s requires a passphrase. Please enter it below.", msg.KeyPath)
		m.viewport.SetContent(m.wrapContent(m.content))
		m.viewport.GotoBottom()
		return m, textinput.Blink
	}

	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	view := m.viewport.View()
	
	// Show passphrase input if needed
	if m.needsPassphrase {
		view += "\n"
		view += lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Enter passphrase: ")
		view += m.passphraseInput.View()
		view += "\n"
		view += lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  Press Enter to submit, Esc to cancel")
	}
	
	// Show command input in terminal mode (only show if terminalMode is true)
	if m.terminalMode {
		view += "\n"
		// Color code prompt based on shell mode - just show "$ "
		var promptColor string
		if m.shellMode == LocalShell {
			promptColor = rustCrab
		} else {
			promptColor = gopherBlue
		}
		view += lipgloss.NewStyle().Foreground(lipgloss.Color(promptColor)).Render("$ ")
		view += m.commandInput.View()
		
		// Show status message below command prompt if present
		if m.statusMessage != "" {
			view += "\n"
			// Color code status message based on type
			var statusColor string
			if strings.HasPrefix(m.statusMessage, "[ERROR]") {
				statusColor = "1" // Red
			} else if strings.HasPrefix(m.statusMessage, "[SUCCESS]") {
				statusColor = "2" // Green
			} else if strings.HasPrefix(m.statusMessage, "[INFO]") {
				statusColor = "241" // Gray
			} else {
				statusColor = "241"
			}
			view += lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(m.statusMessage)
		}
	}
	
	view += m.helpView()
	return view
}

func (m Model) helpView() string {
	if m.terminalMode {
		modeHint := "Remote"
		if m.shellMode == LocalShell {
			modeHint = "Local"
		}
		vimHint := "Insert"
		if m.vimMode == NormalMode {
			vimHint = "Normal"
		}
		return helpStyle(fmt.Sprintf("\n  %s Mode (%s): Type commands • Shift+Tab: Switch shell • Esc: Vim mode • Ctrl+C: Interrupt • q: Quit (normal mode)\n", modeHint, vimHint))
	}
	return helpStyle("\n  ↑/↓: Scroll • ctrl+u/ctrl+d: Page • q: Quit\n")
}

// URL pattern to detect URLs
var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

// wrapContent wraps the content to fit within the viewport width
// URLs are preserved and not broken across lines
func (m Model) wrapContent(content string) string {
	width := m.viewport.Width
	if width <= 0 {
		// Fallback to a reasonable default if width not set
		width = 80
	}

	// Split content by lines, wrap each line, then rejoin
	lines := strings.Split(content, "\n")
	wrappedLines := make([]string, 0, len(lines))

	for _, line := range lines {
		if len(line) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}

		// Check if line contains URLs
		urls := urlPattern.FindAllString(line, -1)
		if len(urls) > 0 {
			// Preserve URLs - wrap around them
			wrapped := wrapLinePreservingURLs(line, width)
			wrappedLines = append(wrappedLines, wrapped)
		} else {
			// Regular wrapping for lines without URLs
			wrapped := wordwrap.String(line, width)
			wrappedLines = append(wrappedLines, wrapped)
		}
	}

	return strings.Join(wrappedLines, "\n")
}

// wrapLinePreservingURLs wraps a line while preserving URLs intact
func wrapLinePreservingURLs(line string, width int) string {
	// Find all URLs in the line
	urlMatches := urlPattern.FindAllStringIndex(line, -1)
	if len(urlMatches) == 0 {
		return wordwrap.String(line, width)
	}

	var result strings.Builder
	lastEnd := 0

	for _, match := range urlMatches {
		start, end := match[0], match[1]
		url := line[start:end]

		// Wrap text before URL
		if start > lastEnd {
			beforeText := line[lastEnd:start]
			wrappedBefore := wordwrap.String(beforeText, width)
			result.WriteString(wrappedBefore)
		}

		// Ensure URL starts on a new line if current line has content
		currentLine := result.String()
		lastNewline := strings.LastIndex(currentLine, "\n")
		currentLineLength := len(currentLine)
		if lastNewline >= 0 {
			currentLineLength = len(currentLine) - lastNewline - 1
		}

		// If current line has content and URL won't fit, start new line
		if currentLineLength > 0 {
			if currentLineLength+len(url)+1 > width {
				result.WriteString("\n")
			} else {
				// Add space before URL if there's room
				result.WriteString(" ")
			}
		}

		// Add URL (keep it intact, even if longer than width)
		result.WriteString(url)

		lastEnd = end
	}

	// Wrap remaining text after last URL
	if lastEnd < len(line) {
		afterText := line[lastEnd:]
		// Check if we need a newline before adding remaining text
		currentLine := result.String()
		lastNewline := strings.LastIndex(currentLine, "\n")
		currentLineLength := len(currentLine)
		if lastNewline >= 0 {
			currentLineLength = len(currentLine) - lastNewline - 1
		}

		if currentLineLength > 0 && currentLineLength+len(afterText) > width {
			result.WriteString("\n")
		}

		wrappedAfter := wordwrap.String(afterText, width)
		result.WriteString(wrappedAfter)
	}

	return result.String()
}

// wrapTerminalContent wraps terminal content to fit within the viewport width
// Filters out problematic control sequences and wraps content
func (m Model) wrapTerminalContent(content string) string {
	width := m.viewport.Width
	if width <= 0 {
		// Fallback to a reasonable default if width not set
		width = 80
	}

	// Filter out carriage returns that might reset cursor position
	// Replace \r\n with \n, and standalone \r with \n
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Split by lines and process each line
	lines := strings.Split(content, "\n")
	wrappedLines := make([]string, 0, len(lines))

	for _, line := range lines {
		if len(line) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}

		// Use wordwrap to wrap the line
		// This handles ANSI codes better than simple character counting
		wrapped := wordwrap.String(line, width)
		wrappedLines = append(wrappedLines, wrapped)
	}

	return strings.Join(wrappedLines, "\n")
}

// tick returns a command that sends a tick message after a short delay
func tick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// StartSSHStream starts streaming SSH output in the background
func (m *Model) StartSSHStream(
	ctx context.Context,
	instance deploy.Instance,
	command string,
	credentialsPath string,
	sshKeyPath string,
) tea.Cmd {
	return m.StartSSHStreamWithPassphrase(ctx, instance, command, credentialsPath, sshKeyPath, "")
}

// StartSSHStreamWithPassphrase starts streaming SSH output with a passphrase
func (m *Model) StartSSHStreamWithPassphrase(
	ctx context.Context,
	instance deploy.Instance,
	command string,
	credentialsPath string,
	sshKeyPath string,
	passphrase string,
) tea.Cmd {
	return func() tea.Msg {
		session, err := deploy.VMConnectWithKey(ctx, instance, sshKeyPath, credentialsPath, passphrase)
		if err != nil {
			return SSHErrorMsg{Error: err}
		}

		m.session = session
		m.outputCh = make(chan []byte, 100)
		m.errCh = make(chan error, 1)

		// Start streaming in background
		go func() {
			err := session.ExecuteStream(command, m.outputCh)
			if err != nil {
				m.errCh <- err
			}
		}()

		// Return a success message to indicate connection was established
		return SSHOutputMsg{Data: []byte("[SUCCESS] Connected to VM. Executing command...\n")}
	}
}

// StartTerminalSession starts an interactive terminal session
func (m *Model) StartTerminalSession(
	ctx context.Context,
	instance deploy.Instance,
	command string,
	credentialsPath string,
	sshKeyPath string,
	passphrase string,
) tea.Cmd {
	return func() tea.Msg {
		termSession, err := deploy.VMConnectTerminal(ctx, instance, sshKeyPath, credentialsPath, passphrase)
		if err != nil {
			return SSHErrorMsg{Error: err}
		}

		// Don't store session here - pass it in the message
		// The session will be stored when TerminalConnectedMsg is processed

		// Start reading terminal output immediately in background
		// This must happen before returning the message
		// Read from stdout
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Don't close channel on panic
				}
			}()
			buffer := make([]byte, 4096)
			for {
				n, err := termSession.Read(buffer)
				if n > 0 {
					data := make([]byte, n)
					copy(data, buffer[:n])
					select {
					case m.terminalOutputCh <- data:
					case <-ctx.Done():
						return
					default:
						// Channel full, skip this chunk (shouldn't happen with buffered channel)
					}
				}
				if err != nil {
					if err != io.EOF && m.debug {
						// Log error for debugging (but not EOF which is normal on close)
						select {
						case m.terminalOutputCh <- []byte(fmt.Sprintf("\n[TERMINAL READ ERROR] %v\n", err)):
						default:
						}
					}
					return
				}
			}
		}()
		
		// Also read from stderr in a separate goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Don't close channel on panic
				}
			}()
			stderrPipe := termSession.StderrPipe()
			if stderrPipe != nil {
				buffer := make([]byte, 4096)
				for {
					n, err := stderrPipe.Read(buffer)
					if n > 0 {
						data := make([]byte, n)
						copy(data, buffer[:n])
						select {
						case m.terminalOutputCh <- data:
						case <-ctx.Done():
							return
						}
					}
					if err != nil {
						return
					}
				}
			}
		}()

		return TerminalConnectedMsg{Session: termSession}
	}
}

// LocalOutputMsg is sent when local command output arrives
type LocalOutputMsg struct {
	Data []byte
}

// LocalErrorMsg is sent when a local command error occurs
type LocalErrorMsg struct {
	Error error
}

// StartLocalCommand executes a command in the local shell and streams output
func (m *Model) StartLocalCommand(command string) tea.Cmd {
	return func() tea.Msg {
		// Get shell from environment or use default
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}

		// Create command with shell -c
		cmd := exec.Command(shell, "-c", command)
		
		// Get stdout pipe
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return LocalErrorMsg{Error: fmt.Errorf("failed to create stdout pipe: %w", err)}
		}
		
		// Get stderr pipe
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return LocalErrorMsg{Error: fmt.Errorf("failed to create stderr pipe: %w", err)}
		}

		// Start the command
		if err := cmd.Start(); err != nil {
			return LocalErrorMsg{Error: fmt.Errorf("failed to start command: %w", err)}
		}

		// Stream stdout in background
		go func() {
			defer stdoutPipe.Close()
			buffer := make([]byte, 4096)
			for {
				n, err := stdoutPipe.Read(buffer)
				if n > 0 {
					data := make([]byte, n)
					copy(data, buffer[:n])
					select {
					case m.localOutputCh <- data:
					case <-m.ctx.Done():
						return
					}
				}
				if err != nil {
					if err != io.EOF {
						select {
						case m.localErrCh <- err:
						default:
						}
					}
					return
				}
			}
		}()

		// Stream stderr in background
		go func() {
			defer stderrPipe.Close()
			buffer := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buffer)
				if n > 0 {
					data := make([]byte, n)
					copy(data, buffer[:n])
					select {
					case m.localOutputCh <- data:
					case <-m.ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		// Wait for command to complete in background
		go func() {
			err := cmd.Wait()
			if err != nil {
				select {
				case m.localErrCh <- err:
				default:
				}
			}
		}()

		// Don't echo command in LocalOutputMsg - the terminal output will show it naturally
		return LocalOutputMsg{Data: []byte{}}
	}
}

// StartDeploymentScript executes deployment steps sequentially
func (m *Model) StartDeploymentScript() tea.Cmd {
	if len(m.deploymentSteps) == 0 {
		// No deployment steps, mark as complete immediately
		m.deploymentComplete = true
		return func() tea.Msg {
			return DeploymentCompleteMsg{}
		}
	}

	m.deploymentRunning = true
	m.currentStep = 0

	// Start with first step
	return m.executeDeploymentStep(0)
}

// executeDeploymentStep executes a single deployment step
func (m *Model) executeDeploymentStep(stepIndex int) tea.Cmd {
	if stepIndex >= len(m.deploymentSteps) {
		// All steps complete
		m.deploymentRunning = false
		m.deploymentComplete = true
		return func() tea.Msg {
			return DeploymentCompleteMsg{}
		}
	}

	step := m.deploymentSteps[stepIndex]
	m.currentStep = stepIndex

	// Send step start message
	stepMsg := DeploymentStepMsg{
		StepNum: stepIndex + 1,
		Total:   len(m.deploymentSteps),
		Step:    step,
	}

	// Execute the step based on target
	if step.Target == "local" {
		// Execute locally
		return tea.Batch(
			func() tea.Msg { return stepMsg },
			m.StartLocalCommand(step.Command),
		)
	} else {
		// Execute remotely via SSH terminal
		if m.terminalSession == nil {
			// Terminal not ready yet, return error
			return func() tea.Msg {
				return LocalErrorMsg{Error: fmt.Errorf("remote step requires SSH connection")}
			}
		}
		
		// Send command to remote terminal
		commandWithNewline := step.Command + "\n"
		if err := m.terminalSession.Write([]byte(commandWithNewline)); err != nil {
			return func() tea.Msg {
				return LocalErrorMsg{Error: fmt.Errorf("failed to send remote command: %w", err)}
			}
		}

		return func() tea.Msg {
			return stepMsg
		}
	}
}

// ContinueDeployment moves to the next deployment step
func (m *Model) ContinueDeployment() tea.Cmd {
	if !m.deploymentRunning {
		return nil
	}

	// Wait a bit for current step output to finish, then continue
	return tea.Sequence(
		tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
			return tickMsg{}
		}),
		m.executeDeploymentStep(m.currentStep + 1),
	)
}

