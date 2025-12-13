//go:build !windows
// +build !windows

package impersonation

import (
	"context"
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// impersonate performs Linux user impersonation using setuid/setgid.
// Note: This requires the agent to run as root.
func (m *Manager) impersonate(ctx context.Context, cred *Credential, logonType int) (*Context, error) {
	// On Linux, we don't do thread-level impersonation like Windows
	// Instead, we'll use setuid for process creation
	// For now, return a no-op context since most actions on Linux
	// will use CreateProcessAsUser instead

	m.logger.Warn().
		Str("user", cred.Username).
		Msg("Thread impersonation not supported on Linux, actions will run as service account")

	return &Context{
		User:      cred.Username,
		LogonType: logonType,
		revertFn:  func() error { return nil },
	}, nil
}

// CreateProcessAsUser creates a new process running as the specified user.
// On Linux, this uses setuid/setgid via exec.Cmd.SysProcAttr.
func (m *Manager) CreateProcessAsUser(ctx context.Context, username string, logonType int, cmdLine string) (*ProcessInfo, error) {
	if !m.config.Enabled {
		return nil, fmt.Errorf("impersonation is disabled")
	}

	// Look up user
	_, uname := ParseDomainUser(username)
	u, err := user.Lookup(uname)
	if err != nil {
		return nil, fmt.Errorf("user lookup failed for %s: %w", uname, err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid uid: %w", err)
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid gid: %w", err)
	}

	m.logger.Info().
		Str("user", username).
		Uint64("uid", uid).
		Uint64("gid", gid).
		Str("cmd", cmdLine).
		Msg("Creating process as user")

	// Parse command line (simple split, doesn't handle quotes)
	// For production, use a proper shell parser
	args := splitCommandLine(cmdLine)
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command line")
	}

	// Create command with setuid/setgid
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	m.logger.Info().
		Str("user", username).
		Int("pid", cmd.Process.Pid).
		Msg("Process created as user")

	return &ProcessInfo{
		Cmd: cmd,
		PID: cmd.Process.Pid,
	}, nil
}

// ProcessInfo holds information about a created process on Linux.
type ProcessInfo struct {
	Cmd *exec.Cmd
	PID int
}

// Wait waits for the process to exit and returns the exit code.
func (p *ProcessInfo) Wait() (uint32, error) {
	err := p.Cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return uint32(exitErr.ExitCode()), nil
		}
		return 0, err
	}
	return 0, nil
}

// Close is a no-op on Linux (process handles are not needed).
func (p *ProcessInfo) Close() error {
	return nil
}

// splitCommandLine splits a command line into arguments.
// This is a simple implementation - for production, use a proper shell parser.
func splitCommandLine(cmdLine string) []string {
	var args []string
	var current string
	var inQuote bool
	var quoteChar rune

	for _, r := range cmdLine {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current += string(r)
			}
		case r == ' ' && !inQuote:
			if current != "" {
				args = append(args, current)
				current = ""
			}
		default:
			current += string(r)
		}
	}

	if current != "" {
		args = append(args, current)
	}

	return args
}
