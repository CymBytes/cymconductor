//go:build windows
// +build windows

package impersonation

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modadvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	moduserenv  = windows.NewLazySystemDLL("userenv.dll")

	procLogonUserW              = modadvapi32.NewProc("LogonUserW")
	procImpersonateLoggedOnUser = modadvapi32.NewProc("ImpersonateLoggedOnUser")
	procRevertToSelf            = modadvapi32.NewProc("RevertToSelf")
	procCreateProcessAsUserW    = modadvapi32.NewProc("CreateProcessAsUserW")
	procCreateEnvironmentBlock  = moduserenv.NewProc("CreateEnvironmentBlock")
	procDestroyEnvironmentBlock = moduserenv.NewProc("DestroyEnvironmentBlock")
)

const (
	LOGON32_PROVIDER_DEFAULT = 0
)

// impersonate performs Windows-specific user impersonation.
func (m *Manager) impersonate(ctx context.Context, cred *Credential, logonType int) (*Context, error) {
	// Convert strings to UTF16 pointers
	domainPtr, err := syscall.UTF16PtrFromString(cred.Domain)
	if err != nil {
		return nil, fmt.Errorf("invalid domain: %w", err)
	}

	usernamePtr, err := syscall.UTF16PtrFromString(cred.Username)
	if err != nil {
		return nil, fmt.Errorf("invalid username: %w", err)
	}

	passwordPtr, err := syscall.UTF16PtrFromString(cred.Password)
	if err != nil {
		return nil, fmt.Errorf("invalid password: %w", err)
	}

	// Call LogonUser to authenticate and get a token
	var token windows.Token
	ret, _, lastErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(usernamePtr)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(passwordPtr)),
		uintptr(logonType),
		uintptr(LOGON32_PROVIDER_DEFAULT),
		uintptr(unsafe.Pointer(&token)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("LogonUser failed: %w", lastErr)
	}

	m.logger.Debug().
		Str("domain", cred.Domain).
		Str("username", cred.Username).
		Int("logon_type", logonType).
		Msg("LogonUser succeeded")

	// Impersonate the logged-on user
	ret, _, lastErr = procImpersonateLoggedOnUser.Call(uintptr(token))
	if ret == 0 {
		token.Close()
		return nil, fmt.Errorf("ImpersonateLoggedOnUser failed: %w", lastErr)
	}

	m.logger.Debug().Msg("ImpersonateLoggedOnUser succeeded")

	return &Context{
		User:      fmt.Sprintf("%s\\%s", cred.Domain, cred.Username),
		LogonType: logonType,
		revertFn: func() error {
			ret, _, lastErr := procRevertToSelf.Call()
			token.Close()
			if ret == 0 {
				return fmt.Errorf("RevertToSelf failed: %w", lastErr)
			}
			return nil
		},
	}, nil
}

// CreateProcessAsUser creates a new process running as the specified user.
// This is used for simulate_process_activity when impersonation is required.
func (m *Manager) CreateProcessAsUser(ctx context.Context, user string, logonType int, cmdLine string) (*ProcessInfo, error) {
	if !m.config.Enabled {
		return nil, fmt.Errorf("impersonation is disabled")
	}

	cred, err := m.GetCredential(user)
	if err != nil {
		return nil, err
	}

	m.logger.Info().
		Str("user", user).
		Str("cmd", cmdLine).
		Msg("Creating process as user")

	// Convert strings to UTF16 pointers
	domainPtr, _ := syscall.UTF16PtrFromString(cred.Domain)
	usernamePtr, _ := syscall.UTF16PtrFromString(cred.Username)
	passwordPtr, _ := syscall.UTF16PtrFromString(cred.Password)
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)
	desktopPtr, _ := syscall.UTF16PtrFromString("winsta0\\default")

	// LogonUser to get token
	var token windows.Token
	ret, _, lastErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(usernamePtr)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(passwordPtr)),
		uintptr(logonType),
		uintptr(LOGON32_PROVIDER_DEFAULT),
		uintptr(unsafe.Pointer(&token)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("LogonUser failed: %w", lastErr)
	}
	defer token.Close()

	// Create environment block for the user
	var envBlock uintptr
	ret, _, lastErr = procCreateEnvironmentBlock.Call(
		uintptr(unsafe.Pointer(&envBlock)),
		uintptr(token),
		0, // Don't inherit current environment
	)
	if ret == 0 {
		return nil, fmt.Errorf("CreateEnvironmentBlock failed: %w", lastErr)
	}
	defer procDestroyEnvironmentBlock.Call(envBlock)

	// Setup startup info
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Desktop = desktopPtr

	var pi windows.ProcessInformation

	// CreateProcessAsUser
	ret, _, lastErr = procCreateProcessAsUserW.Call(
		uintptr(token),
		0, // lpApplicationName - use cmdLine instead
		uintptr(unsafe.Pointer(cmdLinePtr)),
		0, // lpProcessAttributes
		0, // lpThreadAttributes
		0, // bInheritHandles
		uintptr(windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NEW_CONSOLE),
		envBlock,
		0, // lpCurrentDirectory
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CreateProcessAsUser failed: %w", lastErr)
	}

	// Close thread handle, keep process handle
	windows.CloseHandle(pi.Thread)

	m.logger.Info().
		Str("user", user).
		Uint32("pid", pi.ProcessId).
		Msg("Process created as user")

	return &ProcessInfo{
		Handle:    pi.Process,
		ProcessID: pi.ProcessId,
		ThreadID:  pi.ThreadId,
	}, nil
}

// ProcessInfo holds information about a created process.
type ProcessInfo struct {
	Handle    windows.Handle
	ProcessID uint32
	ThreadID  uint32
}

// Wait waits for the process to exit and returns the exit code.
func (p *ProcessInfo) Wait() (uint32, error) {
	_, err := windows.WaitForSingleObject(p.Handle, windows.INFINITE)
	if err != nil {
		return 0, fmt.Errorf("WaitForSingleObject failed: %w", err)
	}

	var exitCode uint32
	err = windows.GetExitCodeProcess(p.Handle, &exitCode)
	if err != nil {
		return 0, fmt.Errorf("GetExitCodeProcess failed: %w", err)
	}

	return exitCode, nil
}

// Close releases the process handle.
func (p *ProcessInfo) Close() error {
	return windows.CloseHandle(p.Handle)
}
