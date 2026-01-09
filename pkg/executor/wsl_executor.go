package executor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"python-manager/pkg/config"
	"python-manager/pkg/wsl"
)

// WSLPythonExecutor executes Python scripts using WSL server via gRPC
type WSLPythonExecutor struct {
	config         *config.PythonConfig
	pythonPath     string
	args           []string
	captureOutput  bool
	autoExit       bool
	interactive    bool
	lastOutput     string
	lastError      string
}

// NewWSLPythonExecutor creates a new WSL Python executor
func NewWSLPythonExecutor(cfg *config.PythonConfig) *WSLPythonExecutor {
	return &WSLPythonExecutor{
		config:        cfg,
		captureOutput: true,
		autoExit:      true,
		interactive:   true, // Default to interactive mode
	}
}

// SetArguments sets the execution arguments
func (e *WSLPythonExecutor) SetArguments(arguments []string) {
	e.args = arguments
}

// SetCaptureOutput sets whether to capture output
func (e *WSLPythonExecutor) SetCaptureOutput(capture bool) {
	e.captureOutput = capture
}

// SetAutoExit sets whether to auto exit
func (e *WSLPythonExecutor) SetAutoExit(autoExit bool) {
	e.autoExit = autoExit
}

// SetInteractive sets whether the script requires interactive input
func (e *WSLPythonExecutor) SetInteractive(interactive bool) {
	e.interactive = interactive
}

// Execute executes the Python script using WSL gRPC
func (e *WSLPythonExecutor) Execute() int {
	// Check if Python path is set
	if e.pythonPath == "" && len(e.args) > 0 {
		// Try to find Python script in arguments to determine which Python environment to use
		for i, arg := range e.args {
			if strings.HasSuffix(arg, ".py") {
				// For WSL execution, we'll use the first available WSL environment or default to a WSL Python path
				e.pythonPath = "/usr/bin/python3" // Default WSL Python path
				// Create new arguments without the Python script path since it's now the pythonPath
				e.args = append(e.args[:i], e.args[i+1:]...)
				break
			}
		}
	}

	if e.pythonPath == "" {
		fmt.Fprintln(os.Stderr, "错误: 未设置Python路径")
		return 1
	}

	// For interactive scripts, execute directly via wsl command for proper stdin support
	if e.interactive {
		return e.executeInteractive()
	}

	// Connect to WSL gRPC server for non-interactive scripts
	client, err := wsl.NewWSLClient("localhost:50051")
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法连接到WSL服务器: %v\n", err)
		return 1
	}
	defer client.Close()

	// Prepare arguments for WSL execution
	allArgs := []string{e.pythonPath}
	allArgs = append(allArgs, e.args...)

	// Execute Python script via WSL
	exitCode, output, errorStr, err := client.ExecutePython(e.pythonPath, e.args, e.captureOutput, e.autoExit, e.interactive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 执行失败: %v\n", err)
		return 1
	}

	// Print output if captured
	if e.captureOutput {
		fmt.Print(output)
	}

	// Print error if exists
	if errorStr != "" {
		fmt.Fprintln(os.Stderr, errorStr)
	}

	return int(exitCode)
}

// executeInteractive executes Python script directly via wsl command for proper interactive support
// This bypasses the gRPC server and directly uses wsl.exe to run the command
// This ensures that stdin/stdout/stderr are properly connected for input() function to work
func (e *WSLPythonExecutor) executeInteractive() int {
	// Convert Windows Python path to WSL path
	wslPythonPath := wsl.ConvertWindowsPathToWSL(e.pythonPath)

	// Build the wsl command with the Python script and arguments
	var wslArgs []string
	
	// Add Python path
	wslArgs = append(wslArgs, wslPythonPath)
	
	// Convert and add arguments
	// We need to check if arguments are file paths and convert them to WSL paths
	for _, arg := range e.args {
		// Simple heuristic: if it looks like a Windows path (has backslashes or drive letter), convert it
		if strings.Contains(arg, "\\") || (len(arg) >= 2 && arg[1] == ':') {
			wslArgs = append(wslArgs, wsl.ConvertWindowsPathToWSL(arg))
		} else {
			wslArgs = append(wslArgs, arg)
		}
	}

	// Create command directly using wsl.exe
	// This avoids issues with cmd.exe argument parsing and quoting
	cmd := exec.Command("wsl", wslArgs...)

	// Connect stdin/stdout/stderr directly for interactive mode
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set proper environment variables
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"PYTHONIOENCODING=utf-8",
		"PYTHONUNBUFFERED=1",
		"WSLENV=PYTHONIOENCODING:PYTHONUNBUFFERED", // Pass environment variables to WSL
	)

	// Execute the command
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "错误: 执行失败: %v\n", err)
		return 1
	}

	return 0
}

// GetLastOutput returns the last execution output
func (e *WSLPythonExecutor) GetLastOutput() string {
	return e.lastOutput
}

// GetLastError returns the last execution error
func (e *WSLPythonExecutor) GetLastError() string {
	return e.lastError
}

// ExecuteInWSL executes a Python script specifically in WSL
func ExecuteInWSL(pythonPath string, arguments []string, captureOutput bool, autoExit bool) (int32, string, string) {
	client, err := wsl.NewWSLClient("localhost:50051")
	if err != nil {
		return 1, "", fmt.Sprintf("无法连接到WSL服务: %v", err)
	}
	defer client.Close()

	exitCode, output, errorStr, err := client.ExecutePython(pythonPath, arguments, captureOutput, autoExit, false)
	if err != nil {
		return 1, "", fmt.Sprintf("执行失败: %v", err)
	}

	return exitCode, output, errorStr
}