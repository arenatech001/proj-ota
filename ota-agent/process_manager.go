package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessManager manages a process with monitoring and auto-restart
type ProcessManager struct {
	exe          string
	args         []string
	workDir      string
	cmdline      string // legacy: when exe is empty, split Fields(cmdline)
	cmd          *exec.Cmd
	ctx          context.Context
	cancel       context.CancelFunc
	logger       *Logger
	mu           sync.Mutex
	running      bool
	restartCount int
	maxRestarts  int
	restartDelay time.Duration
	stopChan     chan struct{}
	stopped      bool
}

// NewProcessManager creates a new process manager (legacy: shell-like single string split by Fields)
func NewProcessManager(cmdline string, logger *Logger) *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProcessManager{
		cmdline:      cmdline,
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
		maxRestarts:  -1, // -1 means unlimited
		restartDelay: 3 * time.Second,
		stopChan:     make(chan struct{}),
	}
}

// NewProcessManagerFromSpec uses argv without invoking the shell.
func NewProcessManagerFromSpec(exe string, args []string, workDir string, logger *Logger) *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())
	cp := append([]string(nil), args...)
	return &ProcessManager{
		exe:          strings.TrimSpace(exe),
		args:         cp,
		workDir:      strings.TrimSpace(workDir),
		cmdline:      "",
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
		maxRestarts:  -1,
		restartDelay: 3 * time.Second,
		stopChan:     make(chan struct{}),
	}
}

func (pm *ProcessManager) displayCommand() string {
	if pm.exe != "" {
		return strings.Join(append([]string{pm.exe}, pm.args...), " ")
	}
	return pm.cmdline
}

// SetMaxRestarts sets the maximum number of restarts (-1 for unlimited)
func (pm *ProcessManager) SetMaxRestarts(max int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.maxRestarts = max
}

// SetRestartDelay sets the delay before restarting after a crash
func (pm *ProcessManager) SetRestartDelay(delay time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.restartDelay = delay
}

// Start starts the process and begins monitoring
func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return fmt.Errorf("process already running")
	}
	pm.running = true
	pm.stopped = false
	pm.mu.Unlock()

	go pm.monitor()
	return nil
}

// Stop stops the process gracefully
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	if !pm.running || pm.stopped {
		pm.mu.Unlock()
		return nil
	}
	pm.stopped = true
	pm.mu.Unlock()

	pm.cancel()
	close(pm.stopChan)

	pm.mu.Lock()
	if pm.cmd != nil && pm.cmd.Process != nil {
		// Try graceful shutdown first (SIGTERM)
		pm.cmd.Process.Signal(syscall.SIGTERM)
		pm.mu.Unlock()

		// Wait a bit for graceful shutdown
		select {
		case <-time.After(5 * time.Second):
			pm.mu.Lock()
			if pm.cmd != nil && pm.cmd.Process != nil {
				// Force kill if still running
				pm.cmd.Process.Kill()
			}
			pm.mu.Unlock()
		case <-pm.ctx.Done():
		}
	} else {
		pm.mu.Unlock()
	}

	pm.mu.Lock()
	pm.running = false
	pm.mu.Unlock()

	return nil
}

// IsRunning returns whether the process is currently running
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running && !pm.stopped
}

// GetRestartCount returns the number of times the process has been restarted
func (pm *ProcessManager) GetRestartCount() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.restartCount
}

// startProcess starts a new process instance
func (pm *ProcessManager) startProcess() error {
	var argv0 string
	var argvRest []string
	if pm.exe != "" {
		argv0 = pm.exe
		argvRest = pm.args
		if argv0 == "" {
			return fmt.Errorf("empty executable")
		}
	} else {
		parts := strings.Fields(pm.cmdline)
		if len(parts) == 0 {
			return fmt.Errorf("empty command")
		}
		argv0 = parts[0]
		argvRest = parts[1:]
	}

	pm.mu.Lock()
	pm.cmd = exec.CommandContext(pm.ctx, argv0, argvRest...)
	if pm.workDir != "" {
		pm.cmd.Dir = pm.workDir
	}
	pm.cmd.Stdout = os.Stdout
	pm.cmd.Stderr = os.Stderr
	pm.mu.Unlock()

	pm.logger.Info("starting process: %s", pm.displayCommand())
	if err := pm.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	return nil
}

// monitor monitors the process and restarts it if it crashes
func (pm *ProcessManager) monitor() {
	for {
		// Check if we should stop
		pm.mu.Lock()
		shouldStop := pm.stopped
		pm.mu.Unlock()

		if shouldStop {
			return
		}

		// Start the process
		if err := pm.startProcess(); err != nil {
			pm.logger.Error("failed to start process: %v", err)
			pm.mu.Lock()
			pm.running = false
			pm.mu.Unlock()
			return
		}

		// Wait for process to exit
		err := pm.cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
		}

		pm.mu.Lock()
		shouldStop = pm.stopped
		pm.mu.Unlock()

		if shouldStop {
			pm.logger.Info("process stopped by user")
			return
		}

		// Process exited unexpectedly
		if exitCode != 0 {
			pm.logger.Warn("process exited with code %d, will restart", exitCode)
		} else {
			pm.logger.Warn("process exited normally, will restart")
		}

		// Check restart limit
		pm.mu.Lock()
		if pm.maxRestarts >= 0 && pm.restartCount >= pm.maxRestarts {
			pm.logger.Error("max restarts (%d) reached, stopping process", pm.maxRestarts)
			pm.running = false
			pm.mu.Unlock()
			return
		}
		pm.restartCount++
		restartDelay := pm.restartDelay
		pm.mu.Unlock()

		// Wait before restarting
		pm.logger.Info("restarting process in %v (restart count: %d)", restartDelay, pm.restartCount)
		select {
		case <-time.After(restartDelay):
			// Continue to restart
		case <-pm.ctx.Done():
			return
		case <-pm.stopChan:
			return
		}
	}
}

// Global process manager instance
var (
	globalProcessManager *ProcessManager
	globalProcessCmdline string
	processManagerMutex  sync.Mutex
)

// startManagedProcess starts a process with monitoring and auto-restart
// If the same command is already running, it will be restarted
func startManagedProcess(cmdline string, logger *Logger) (*ProcessManager, error) {
	if cmdline == "" {
		return nil, fmt.Errorf("empty command")
	}

	processManagerMutex.Lock()
	defer processManagerMutex.Unlock()

	// If same command is already running, restart it
	if globalProcessManager != nil && globalProcessCmdline == cmdline {
		if globalProcessManager.IsRunning() {
			logger.Info("process already running with same command, restarting...")
			globalProcessManager.Stop()
		}
	} else if globalProcessManager != nil {
		// Different command, stop old one
		logger.Info("restart command changed, stopping old process...")
		globalProcessManager.Stop()
	}

	// Create and start new process manager
	pm := NewProcessManager(cmdline, logger)
	if err := pm.Start(); err != nil {
		return nil, err
	}

	globalProcessManager = pm
	globalProcessCmdline = cmdline
	return pm, nil
}

// ensureManagedProcess ensures the managed process is running
// Returns true if process was started or already running, false if startCmd is empty
func ensureManagedProcess(startCmd string, reason string, logger *Logger) (bool, error) {
	if startCmd == "" {
		return false, nil
	}

	processManagerMutex.Lock()
	shouldStart := globalProcessManager == nil || !globalProcessManager.IsRunning() || globalProcessCmdline != startCmd
	processManagerMutex.Unlock()

	if !shouldStart {
		logger.Info("managed process already running: %s", startCmd)
		return true, nil
	}

	logger.Info("ensuring managed process is running (%s): %s", reason, startCmd)
	pm, err := startManagedProcess(startCmd, logger)
	if err != nil {
		logger.Error("failed to start managed process: %v", err)
		return false, fmt.Errorf("failed to start managed process: %w", err)
	}
	logger.Info("process manager started (restart count: %d)", pm.GetRestartCount())
	return true, nil
}

// stopManagedProcess stops the managed process
func stopManagedProcess() error {
	processManagerMutex.Lock()
	defer processManagerMutex.Unlock()

	if globalProcessManager != nil {
		err := globalProcessManager.Stop()
		globalProcessManager = nil
		globalProcessCmdline = ""
		return err
	}
	return nil
}
