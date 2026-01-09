//go:build linux || unix
// +build linux unix

package wsl

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	proto "python-manager/proto"

	"google.golang.org/grpc"
)

// ProcessManager manages running Python processes
type ProcessManager struct {
	mu       sync.Mutex
	commands map[string]*exec.Cmd
}

var processManager = &ProcessManager{
	commands: make(map[string]*exec.Cmd),
}

// AddCommand adds a command to the manager
func (pm *ProcessManager) AddCommand(id string, cmd *exec.Cmd) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Clean up any existing command with the same ID
	if existingCmd, exists := pm.commands[id]; exists {
		pm.cleanupCommand(existingCmd)
	}

	pm.commands[id] = cmd
}

// RemoveCommand removes a command from the manager
func (pm *ProcessManager) RemoveCommand(id string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if cmd, exists := pm.commands[id]; exists {
		pm.cleanupCommand(cmd)
		delete(pm.commands, id)
	}
}

// cleanupCommand cleans up a command and its resources
func (pm *ProcessManager) cleanupCommand(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	// Try to terminate the process group
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		// Unix-like systems: use process group
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil {
			// Kill the entire process group
			syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			// Fallback: kill just the process
			cmd.Process.Kill()
		}

		// Wait a bit for graceful termination
		time.Sleep(100 * time.Millisecond)

		// If still running, force kill
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			if err == nil {
				syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				cmd.Process.Kill()
			}
		}
	}
}

// CleanupAll cleans up all running commands
func (pm *ProcessManager) CleanupAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for id, cmd := range pm.commands {
		pm.cleanupCommand(cmd)
		delete(pm.commands, id)
	}
}

// WSLPythonServer implements the PythonService gRPC server
type WSLPythonServer struct {
	proto.UnimplementedPythonServiceServer
}

// SearchEnvironments searches for Python environments in WSL
func (s *WSLPythonServer) SearchEnvironments(ctx context.Context, req *proto.SearchEnvironmentsRequest) (*proto.SearchEnvironmentsResponse, error) {
	log.Println("Searching for Python environments in WSL...")

	var environments []*proto.PythonEnvironment

	// Find system Python
	if pythonPath := findSystemPython(); pythonPath != "" {
		env := &proto.PythonEnvironment{
			Name:     "wsl_system",
			Path:     pythonPath,
			Version:  getPythonVersion(pythonPath),
			Enabled:  true,
			Encoding: "utf-8",
			Platform: "wsl",
		}
		environments = append(environments, env)
		log.Printf("Found system Python: %s", pythonPath)
	}

	// Find Conda environments
	condaEnvs := findCondaEnvironments()
	for i, condaPath := range condaEnvs {
		env := &proto.PythonEnvironment{
			Name:     fmt.Sprintf("wsl_conda_%d", i),
			Path:     condaPath,
			Version:  getPythonVersion(condaPath),
			Enabled:  false, // Conda environments disabled by default
			Encoding: "utf-8",
			Platform: "wsl",
		}
		environments = append(environments, env)
		log.Printf("Found conda environment: %s", condaPath)
	}

	// Find virtual environments
	venvEnvs := findVenvEnvironments()
	for i, venvPath := range venvEnvs {
		env := &proto.PythonEnvironment{
			Name:     fmt.Sprintf("wsl_venv_%d", i),
			Path:     venvPath,
			Version:  getPythonVersion(venvPath),
			Enabled:  false, // Venv environments disabled by default
			Encoding: "utf-8",
			Platform: "wsl",
		}
		environments = append(environments, env)
		log.Printf("Found venv environment: %s", venvPath)
	}

	// Find uv environments
	uvEnvs := findUVEnvironments()
	for i, uvPath := range uvEnvs {
		env := &proto.PythonEnvironment{
			Name:     fmt.Sprintf("wsl_uv_%d", i),
			Path:     uvPath,
			Version:  getPythonVersion(uvPath),
			Enabled:  false, // UV environments disabled by default
			Encoding: "utf-8",
			Platform: "wsl",
		}
		environments = append(environments, env)
		log.Printf("Found uv environment: %s", uvPath)
	}

	log.Printf("Found %d Python environments in WSL", len(environments))
	return &proto.SearchEnvironmentsResponse{
		Environments: environments,
	}, nil
}

// ExecutePython executes a Python script in WSL
func (s *WSLPythonServer) ExecutePython(ctx context.Context, req *proto.ExecutePythonRequest) (*proto.ExecutePythonResponse, error) {
	log.Printf("Executing Python script in WSL: %s", strings.Join(req.Arguments, " "))

	// Check if Python exists
	if req.PythonPath == "" {
		return &proto.ExecutePythonResponse{
			ExitCode: 1,
			Error:    "Python path is empty",
		}, nil
	}

	if _, err := os.Stat(req.PythonPath); os.IsNotExist(err) {
		return &proto.ExecutePythonResponse{
			ExitCode: 1,
			Error:    fmt.Sprintf("Python executable does not exist: %s", req.PythonPath),
		}, nil
	}

	// Build command arguments
	cmdArgs := []string{}
	cmdArgs = append(cmdArgs, req.Arguments...)

	// Create command
	cmd := exec.Command(req.PythonPath, cmdArgs...)

	// Set process group to allow killing the entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set environment variables to ensure UTF-8 encoding
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONLEGACYWINDOWSSTDIO=utf-8",
	)

	// Generate a unique ID for this command
	commandID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Register command with the process manager
	processManager.AddCommand(commandID, cmd)

	// Ensure cleanup when done
	defer processManager.RemoveCommand(commandID)

	// Execute command
	var output []byte
	var err error

	if req.Interactive {
		// For interactive scripts in WSL, we need to handle input/output carefully
		// Use a pseudo-terminal approach if available, otherwise fall back to pipes

		// Create pipes for stdin, stdout, and stderr
		stdinPipe, err := cmd.StdinPipe()
		if err != nil {
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    fmt.Sprintf("Failed to create stdin pipe: %v", err),
			}, nil
		}

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			stdinPipe.Close()
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    fmt.Sprintf("Failed to create stdout pipe: %v", err),
			}, nil
		}

		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			stdinPipe.Close()
			stdoutPipe.Close()
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    fmt.Sprintf("Failed to create stderr pipe: %v", err),
			}, nil
		}

		// Start the command
		if err := cmd.Start(); err != nil {
			stdinPipe.Close()
			stdoutPipe.Close()
			stderrPipe.Close()
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    fmt.Sprintf("Failed to start command: %v", err),
			}, nil
		}

		// Create a context to control the stdin copy goroutine
		stdinCtx, stdinCancel := context.WithCancel(context.Background())

		// For WSL environment, we need to handle input differently
		// If there's input data provided, use it; otherwise, connect stdin for real-time input
		if req.InputData != "" {
			// Write the provided input data to stdin
			if _, err := stdinPipe.Write([]byte(req.InputData)); err != nil {
				log.Printf("Warning: Failed to write input data: %v", err)
			}
			stdinPipe.Close()
			stdinCancel()
		} else {
			// For truly interactive scripts, we need to connect stdin directly
			// This allows real-time input from the user, but only if stdin is available
			go func() {
				defer func() {
					stdinPipe.Close()
					stdinCancel()
				}()
				// Check if stdin is connected and copy from it to the process stdin
				// Use a non-blocking approach to avoid hanging when no stdin is available
				stat, err := os.Stdin.Stat()
				if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
					// Stdin is not connected or is connected to a terminal
					// In this case, just let the process run without stdin connection
					return
				}
				// Check if context is cancelled before copying
				select {
				case <-stdinCtx.Done():
					return
				default:
				}
				// Stdin is connected, copy data with context cancellation support
				io.Copy(stdinPipe, os.Stdin)
			}()
		}

		// Read all output from stdout and stderr with proper cancellation support
		stdoutChan := make(chan []byte, 1)
		stderrChan := make(chan []byte, 1)
		var stdoutBytes, stderrBytes []byte

		go func() {
			var err error
			stdoutBytes, err = ioutil.ReadAll(stdoutPipe)
			if err != nil {
				log.Printf("Error reading stdout: %v", err)
			}
			stdoutChan <- stdoutBytes
		}()

		go func() {
			var err error
			stderrBytes, err = ioutil.ReadAll(stderrPipe)
			if err != nil {
				log.Printf("Error reading stderr: %v", err)
			}
			stderrChan <- stderrBytes
		}()

		// Wait for command to finish with timeout
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		// Set a timeout based on the context or default to 30 minutes
		timeout := 30 * time.Minute
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
		}

		var cmdErr error
		select {
		case cmdErr = <-done:
			// Command completed normally
		case <-time.After(timeout):
			// Command timed out
			log.Printf("Command timed out, terminating...")
			stdinCancel()
			cmd.Process.Kill()
			<-done // Wait for process to actually terminate
			stdoutBytes = <-stdoutChan
			stderrBytes = <-stderrChan
			processManager.RemoveCommand(commandID)
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    "Command execution timed out",
			}, nil
		case <-ctx.Done():
			// Context was cancelled
			log.Printf("Context cancelled, terminating command...")
			stdinCancel()
			cmd.Process.Kill()
			<-done // Wait for process to actually terminate
			stdoutBytes = <-stdoutChan
			stderrBytes = <-stderrChan
			processManager.RemoveCommand(commandID)
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Error:    "Command execution was cancelled",
			}, nil
		}

		// Wait for output reading to complete (with timeout to prevent hanging)
		select {
		case <-stdoutChan:
		case <-time.After(5 * time.Second):
			log.Printf("Warning: Timeout waiting for stdout")
		}

		select {
		case <-stderrChan:
		case <-time.After(5 * time.Second):
			log.Printf("Warning: Timeout waiting for stderr")
		}

		// Combine stdout and stderr for output
		combinedOutput := string(stdoutBytes) + string(stderrBytes)

		if cmdErr != nil {
			if exitError, ok := cmdErr.(*exec.ExitError); ok {
				return &proto.ExecutePythonResponse{
					ExitCode: int32(exitError.ExitCode()),
					Output:   combinedOutput,
					Error:    cmdErr.Error(),
				}, nil
			}
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Output:   combinedOutput,
				Error:    cmdErr.Error(),
			}, nil
		}

		return &proto.ExecutePythonResponse{
			ExitCode: 0,
			Output:   combinedOutput,
			Error:    "",
		}, nil
	} else if req.CaptureOutput {
		// For non-interactive scripts with capture output, use CombinedOutput
		output, err = cmd.CombinedOutput()
	} else {
		// If not capturing output, run and wait
		err = cmd.Run()
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				return &proto.ExecutePythonResponse{
					ExitCode: int32(exitError.ExitCode()),
					Output:   string(output),
					Error:    err.Error(),
				}, nil
			}
			return &proto.ExecutePythonResponse{
				ExitCode: 1,
				Output:   string(output),
				Error:    err.Error(),
			}, nil
		}
		return &proto.ExecutePythonResponse{
			ExitCode: 0,
			Output:   string(output),
			Error:    "",
		}, nil
	}

	if err != nil {
		// Check exit code if it's an ExitError
		if exitError, ok := err.(*exec.ExitError); ok {
			return &proto.ExecutePythonResponse{
				ExitCode: int32(exitError.ExitCode()),
				Output:   string(output),
				Error:    string(output), // Use combined output as error since it contains error info
			}, nil
		}
		// If it's a different error, return 1
		return &proto.ExecutePythonResponse{
			ExitCode: 1,
			Output:   string(output),
			Error:    err.Error(),
		}, nil
	}

	return &proto.ExecutePythonResponse{
		ExitCode: 0,
		Output:   string(output),
		Error:    "",
	}, nil
}

// findSystemPython finds system Python installations in WSL
func findSystemPython() string {
	// In WSL, Python is typically installed with apt or available in PATH
	possiblePaths := []string{
		"python3",
		"python",
		"/usr/bin/python3",
		"/usr/bin/python",
		"/usr/local/bin/python3",
		"/usr/local/bin/python",
	}

	for _, path := range possiblePaths {
		// Use 'which' to find the executable
		cmd := exec.Command("which", path)
		output, err := cmd.Output()
		if err == nil {
			pythonPath := strings.TrimSpace(string(output))
			if pythonPath != "" {
				// Verify that the file exists
				if _, err := os.Stat(pythonPath); err == nil {
					return pythonPath
				}
			}
		}
	}

	return ""
}

// findCondaEnvironments finds conda environments in WSL
func findCondaEnvironments() []string {
	var condaEnvs []string

	// Check if conda is available
	cmd := exec.Command("which", "conda")
	if err := cmd.Run(); err != nil {
		return condaEnvs // Conda not available
	}

	// Get conda environment list
	cmd = exec.Command("conda", "env", "list")
	output, err := cmd.Output()
	if err != nil {
		return condaEnvs
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip current environment indicator
		if line == "*" || strings.HasPrefix(line, "*") {
			continue
		}

		// Parse environment name and path
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			envName := parts[0]
			envPath := parts[1]

			// Skip if envName is the indicator for current env
			if envName == "*" {
				continue
			}

			// Build Python path
			pythonPath := filepath.Join(strings.Trim(envPath, "* "), "bin", "python")
			if _, err := os.Stat(pythonPath); err == nil {
				condaEnvs = append(condaEnvs, pythonPath)
			}
		}
	}

	// If no environments found via conda command, try common installation paths
	if len(condaEnvs) == 0 {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			// Miniconda
			minicondaPath := filepath.Join(homeDir, "miniconda3", "bin", "python")
			if _, err := os.Stat(minicondaPath); err == nil {
				condaEnvs = append(condaEnvs, minicondaPath)
			}

			// Check envs directory
			envsDir := filepath.Join(homeDir, "miniconda3", "envs")
			if _, err := os.Stat(envsDir); err == nil {
				entries, err := ioutil.ReadDir(envsDir)
				if err == nil {
					for _, entry := range entries {
						if entry.IsDir() {
							pythonPath := filepath.Join(envsDir, entry.Name(), "bin", "python")
							if _, err := os.Stat(pythonPath); err == nil {
								condaEnvs = append(condaEnvs, pythonPath)
							}
						}
					}
				}
			}

			// Anaconda
			anacondaPath := filepath.Join(homeDir, "anaconda3", "bin", "python")
			if _, err := os.Stat(anacondaPath); err == nil {
				condaEnvs = append(condaEnvs, anacondaPath)
			}

			// Check envs directory for Anaconda
			envsDir = filepath.Join(homeDir, "anaconda3", "envs")
			if _, err := os.Stat(envsDir); err == nil {
				entries, err := ioutil.ReadDir(envsDir)
				if err == nil {
					for _, entry := range entries {
						if entry.IsDir() {
							pythonPath := filepath.Join(envsDir, entry.Name(), "bin", "python")
							if _, err := os.Stat(pythonPath); err == nil {
								condaEnvs = append(condaEnvs, pythonPath)
							}
						}
					}
				}
			}
		}
	}

	return condaEnvs
}

// findUVEnvironments finds uv environments in WSL
func findUVEnvironments() []string {
	var uvEnvs []string

	// Look for uv command in PATH
	cmd := exec.Command("which", "uv")
	if err := cmd.Run(); err != nil {
		return uvEnvs // uv not available
	}

	// In WSL, look for uv managed environments
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return uvEnvs
	}

	uvEnvPath := filepath.Join(homeDir, ".local", "share", "uv", "python")
	if _, err := os.Stat(uvEnvPath); err == nil {
		// Walk through the directory to find Python executables
		filepath.Walk(uvEnvPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && info.Name() == "python" {
				uvEnvs = append(uvEnvs, path)
			}
			return nil
		})
	}

	return uvEnvs
}

// findVenvEnvironments finds venv environments in WSL
func findVenvEnvironments() []string {
	var venvEnvs []string

	// Find current directory and search parent directories for venv
	currentPath, _ := os.Getwd()
	currentPath = filepath.Clean(currentPath)

	// Search up to 5 levels of parent directories
	for i := 0; i < 5; i++ {
		// Check common virtual environment directory names
		venvNames := []string{"venv", ".venv", "env", ".env"}

		for _, venvName := range venvNames {
			venvPath := filepath.Join(currentPath, venvName)
			if _, err := os.Stat(venvPath); err == nil {
				pythonPath := filepath.Join(venvPath, "bin", "python")
				if _, err := os.Stat(pythonPath); err == nil {
					venvEnvs = append(venvEnvs, pythonPath)
				}
			}
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			// We've reached the root
			break
		}
		currentPath = parentPath
	}

	// Search home directory for virtual environments
	homeDir, err := os.UserHomeDir()
	if err == nil {
		venvNames := []string{"venv", ".venv", "env", ".env"}
		for _, venvName := range venvNames {
			venvPath := filepath.Join(homeDir, venvName)
			if _, err := os.Stat(venvPath); err == nil {
				pythonPath := filepath.Join(venvPath, "bin", "python")
				if _, err := os.Stat(pythonPath); err == nil {
					venvEnvs = append(venvEnvs, pythonPath)
				}
			}
		}
	}

	// Check VIRTUAL_ENV environment variable
	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv != "" {
		pythonPath := filepath.Join(virtualEnv, "bin", "python")
		if _, err := os.Stat(pythonPath); err == nil {
			venvEnvs = append(venvEnvs, pythonPath)
		}
	}

	return venvEnvs
}

// getPythonVersion gets the Python version from the executable
func getPythonVersion(pythonPath string) string {
	cmd := exec.Command(pythonPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	versionOutput := string(output)
	// Parse version, e.g. from "Python 3.9.7" extract "3.9.7"
	re := regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(versionOutput)
	if len(matches) > 1 {
		return matches[1]
	}

	// Try alternative format "Python 3.9.7+" (with possible revision)
	re = regexp.MustCompile(`Python (\d+\.\d+\.\d+\S*)`)
	matches = re.FindStringSubmatch(versionOutput)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// StartWSLServer starts the gRPC server for WSL
func StartWSLServer(port string) error {
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Cleanup function to be called on shutdown
	cleanup := func() {
		log.Println("Shutting down WSL Python Service server...")
		processManager.CleanupAll()
	}

	// Goroutine to handle signals
	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v", sig)
		cleanup()
		os.Exit(0)
	}()

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	server := &WSLPythonServer{}
	proto.RegisterPythonServiceServer(grpcServer, server)

	log.Printf("Starting WSL Python Service server on port %s...", port)
	if err := grpcServer.Serve(lis); err != nil {
		cleanup()
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}
