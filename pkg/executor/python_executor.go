package executor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"python-manager/pkg/config"
	"python-manager/pkg/utils"
	"python-manager/pkg/wsl"
)

// PythonExecutor executes Python scripts with various options
type PythonExecutor struct {
	config         *config.PythonConfig
	pythonPath     string
	args           []string
	captureOutput  bool
	autoExit       bool
	interactive    bool
	lastOutput     string
	lastError      string
}

// NewPythonExecutor creates a new PythonExecutor instance
func NewPythonExecutor(cfg *config.PythonConfig) *PythonExecutor {
	return &PythonExecutor{
		config:        cfg,
		pythonPath:    cfg.GetCurrentPythonPath(),
		captureOutput: true,
		autoExit:      true,
		interactive:   true, // Default to interactive mode
	}
}

// SetArguments sets the execution arguments
func (e *PythonExecutor) SetArguments(arguments []string) {
	e.args = arguments
}

// SetCaptureOutput sets whether to capture output
func (e *PythonExecutor) SetCaptureOutput(capture bool) {
	e.captureOutput = capture
}

// SetAutoExit sets whether to auto exit
func (e *PythonExecutor) SetAutoExit(autoExit bool) {
	e.autoExit = autoExit
}

// SetInteractive sets whether the script requires interactive input
func (e *PythonExecutor) SetInteractive(interactive bool) {
	e.interactive = interactive
}

// Execute executes the Python script
func (e *PythonExecutor) Execute() int {
	// Display current Python environment info
	e.config.DisplayCurrentEnvironment()

	// Determine if we need to use WSL
	currentEnv := e.config.GetCurrentEnvironment()
	if currentEnv.Platform == "wsl" {
		return e.executeInWSL()
	}

	// Default to Windows execution
	return e.executeOnWindows()
}

// executeOnWindows executes Python script on Windows
func (e *PythonExecutor) executeOnWindows() int {
	// Check if Python exists
	if !utils.FileExists(e.pythonPath) {
		e.lastError = fmt.Sprintf("Python executable does not exist: %s", e.pythonPath)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Check if needs to handle terminal-in-terminal issue
	hasSystemCommand := false
	for _, arg := range e.args {
		if strings.Contains(arg, ".py") {
			// Check Python file content for os.system or subprocess calls
			if utils.FileExists(arg) {
				content, err := os.ReadFile(arg)
				if err == nil {
					contentStr := string(content)
					if strings.Contains(contentStr, "os.system") ||
						strings.Contains(contentStr, "subprocess") ||
						strings.Contains(contentStr, "popen") {
						hasSystemCommand = true
						break
					}
				}
			}
			break
		}
	}

	// Execute Python script
	var exitCode int
	if hasSystemCommand {
		fmt.Println("Detected system command call, using special handling mode...")
		exitCode = e.executeWithTerminalHandling()
	} else {
		exitCode = e.executeWithOutputCapture()
	}

	// If execution failed and captured output, try to detect missing modules and auto install
	if exitCode != 0 && e.lastError != "" {
		fmt.Println("\nScript execution failed, checking for missing modules...")
		exitCode = e.handleMissingModulesAndRetry()
	}

	return exitCode
}

// executeInWSL executes Python script in WSL
func (e *PythonExecutor) executeInWSL() int {
	// If interactive mode is enabled, use WSLPythonExecutor to execute directly via wsl command
	// This ensures that stdin/stdout/stderr are properly connected for input() function to work
	if e.interactive {
		wslExecutor := NewWSLPythonExecutor(e.config)
		wslExecutor.pythonPath = e.pythonPath
		wslExecutor.SetArguments(e.args)
		wslExecutor.SetCaptureOutput(e.captureOutput)
		wslExecutor.SetAutoExit(e.autoExit)
		wslExecutor.SetInteractive(e.interactive)
		return wslExecutor.Execute()
	}

	// Connect to WSL gRPC server
	client, err := wsl.NewWSLClient("localhost:50051")
	if err != nil {
		e.lastError = fmt.Sprintf("Failed to connect to WSL server: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}
	defer client.Close()

	// For WSL environment, we don't pre-collect input data
	// Instead, we rely on the WSL server to handle real-time input
	var inputData string
	
	// Execute Python script via WSL
	exitCode, output, errorStr, err := client.ExecutePythonWithInput(e.pythonPath, e.args, e.captureOutput, e.autoExit, e.interactive, inputData)
	if err != nil {
		e.lastError = fmt.Sprintf("Execution failed: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Print output if captured
	if e.captureOutput {
		fmt.Print(output)
	}

	// Print error if exists
	if errorStr != "" {
		fmt.Fprintln(os.Stderr, errorStr)
		e.lastError = errorStr
	}

	return int(exitCode)
}

// collectInputData collects input data from the user for interactive scripts
func (e *PythonExecutor) collectInputData() string {
	// Check if the script contains input() calls
	if !e.scriptNeedsInput() {
		return ""
	}

	fmt.Println("This script requires input. Please enter the input data (press Ctrl+Z then Enter on Windows or Ctrl+D on Unix when done):")
	
	var inputData strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		inputData.WriteString(scanner.Text())
		inputData.WriteString("\n")
	}
	
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
		return ""
	}
	
	return inputData.String()
}

// scriptNeedsInput checks if the script contains input() calls
func (e *PythonExecutor) scriptNeedsInput() bool {
	// Check each argument to see if it's a Python file
	for _, arg := range e.args {
		if strings.HasSuffix(arg, ".py") && utils.FileExists(arg) {
			// Read the file content
			content, err := os.ReadFile(arg)
			if err != nil {
				continue
			}
			
			// Check if the content contains input() calls
			contentStr := string(content)
			if strings.Contains(contentStr, "input(") {
				return true
			}
		}
	}
	
	return false
}

// executeWithOutputCapture executes Python script and captures output
func (e *PythonExecutor) executeWithOutputCapture() int {
	e.lastOutput = ""
	e.lastError = ""

	// Check if Python exists
	if !utils.FileExists(e.pythonPath) {
		e.lastError = fmt.Sprintf("Python executable does not exist: %s", e.pythonPath)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Build command line
	cmdArgs := []string{}
	cmdArgs = append(cmdArgs, e.args...)

	// Create command
	cmd := exec.Command(e.pythonPath, cmdArgs...)

	// Set environment variables to ensure UTF-8 encoding
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONLEGACYWINDOWSSTDIO=utf-8",
	)

	// Connect stdin to allow user input
	cmd.Stdin = os.Stdin

	// Capture output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.lastError = fmt.Sprintf("Cannot create stdout pipe: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.lastError = fmt.Sprintf("Cannot create stderr pipe: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		e.lastError = fmt.Sprintf("Failed to start process: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Read stdout
	stdoutScanner := bufio.NewScanner(stdout)
	for stdoutScanner.Scan() {
		line := stdoutScanner.Text()
		e.lastOutput += line + "\n"
		// Directly output to console (already UTF-8 encoded)
		fmt.Println(line)
	}

	// Read stderr
	stderrScanner := bufio.NewScanner(stderr)
	for stderrScanner.Scan() {
		line := stderrScanner.Text()
		e.lastError += line + "\n"
		// Directly output to console (already UTF-8 encoded)
		fmt.Fprintln(os.Stderr, line)
	}

	// Wait for command to finish
	err = cmd.Wait()
	if err != nil {
		// Check exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		return 1
	}

	return 0
}

// executeWithTerminalHandling executes Python script with terminal handling
func (e *PythonExecutor) executeWithTerminalHandling() int {
	e.lastOutput = ""
	e.lastError = ""

	// Check if Python exists
	if !utils.FileExists(e.pythonPath) {
		e.lastError = fmt.Sprintf("Python executable does not exist: %s", e.pythonPath)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Build command line
	cmdArgs := []string{}
	cmdArgs = append(cmdArgs, e.args...)

	// Create command
	cmd := exec.Command(e.pythonPath, cmdArgs...)

	// Set environment variables to ensure UTF-8 encoding
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONLEGACYWINDOWSSTDIO=utf-8",
	)

	// Connect stdin to allow user input and use current console for output but capture stderr
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.lastError = fmt.Sprintf("Cannot create stderr pipe: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		e.lastError = fmt.Sprintf("Failed to start process: %v", err)
		fmt.Fprintln(os.Stderr, e.lastError)
		return 1
	}

	// Read stderr in a goroutine
	errChan := make(chan string, 1)
	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		var errorOutput string
		for stderrScanner.Scan() {
			line := stderrScanner.Text()
			errorOutput += line + "\n"
			// Directly output to console (already UTF-8 encoded)
			fmt.Fprintln(os.Stderr, line)
		}
		errChan <- errorOutput
	}()

	// Wait for command to finish or timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var waitResult error
	if e.autoExit {
		waitResult = <-done
	} else {
		select {
		case waitResult = <-done:
		case <-time.After(10 * time.Second): // 10 second timeout
			fmt.Println("\nPython script execution timed out, terminating...")
			if cmd.Process != nil {
				// On Unix-like systems, sending interrupt signal first
				if runtime.GOOS != "windows" {
					if err := cmd.Process.Signal(os.Interrupt); err != nil {
						// If SIGINT fails, kill the process
						if killErr := cmd.Process.Kill(); killErr != nil {
							fmt.Printf("Cannot terminate process: %v\n", killErr)
						}
					}
				} else {
					// On Windows, use our process tree termination function
					if err := utils.TerminateProcessTree(cmd); err != nil {
						fmt.Printf("Cannot terminate process: %v\n", err)
					}
				}
			}
			// Wait a bit more for the process to terminate
			select {
			case waitResult = <-done:
			case <-time.After(5 * time.Second): // Wait up to 5 more seconds
				waitResult = fmt.Errorf("process did not terminate in time")
				if cmd.Process != nil {
					// Force kill if still running after timeout
					_ = cmd.Process.Kill()
				}
			}
		}
	}

	// Get stderr output
	e.lastError = <-errChan

	// Get exit code
	if exitError, ok := waitResult.(*exec.ExitError); ok {
		exitCode := exitError.ExitCode()
		if exitCode != 0 {
			// If error output is empty, try to get exit code info
			if e.lastError == "" {
				e.lastError = fmt.Sprintf("Python script failed, exit code: %d", exitCode)
				fmt.Fprintln(os.Stderr, e.lastError)
			}
		}
		return exitCode
	}

	return 0
}

// GetLastOutput returns the last execution output
func (e *PythonExecutor) GetLastOutput() string {
	return e.lastOutput
}

// GetLastError returns the last execution error
func (e *PythonExecutor) GetLastError() string {
	return e.lastError
}

// detectMissingModules detects missing modules from error output
func (e *PythonExecutor) detectMissingModules(errorOutput string) []string {
	var missingModules []string

	// Common module missing error patterns
	patterns := []*regexp.Regexp{
		// Python standard error: ModuleNotFoundError: No module named 'module_name'
		regexp.MustCompile(`ModuleNotFoundError:\s*No\s+module\s+named\s+['"]([^'"]+)['"]`),
		// ImportError: No module named 'module_name'
		regexp.MustCompile(`ImportError:\s*No\s+module\s+named\s+['"]([^'"]+)['"]`),
		// pip install error: ERROR: Could not find a version that satisfies the requirement module_name
		regexp.MustCompile(`ERROR:\s*Could\s+not\s+find\s+a\s+version\s+that\s+satisfies\s+the\s+requirement\s+(\S+)`),
		// Simple module name pattern
		regexp.MustCompile(`['"]([^'"]+)['"]\s+(?:module|package)\s+(?:not\s+found|is\s+not\s+installed)`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindAllStringSubmatch(errorOutput, -1)
		for _, match := range matches {
			if len(match) > 1 {
				moduleName := match[1]

				// Filter out some obviously non-module name results
				if moduleName != "" &&
					!strings.Contains(moduleName, " ") &&
					!containsString(missingModules, moduleName) {
					missingModules = append(missingModules, moduleName)
				}
			}
		}
	}

	return missingModules
}

// installMissingModules installs missing modules
func (e *PythonExecutor) installMissingModules(modules []string) bool {
	if len(modules) == 0 {
		return true
	}

	fmt.Println("\nDetected missing Python modules:")
	for _, module := range modules {
		fmt.Printf("  - %s\n", module)
	}

	fmt.Println("\nPress Enter to automatically install these modules, or click 'Stop' button to cancel...")
	fmt.Scanln() // Wait for user input

	// Build pip install command
	pipArgs := []string{"-m", "pip", "install"}
	pipArgs = append(pipArgs, modules...)

	// Create command
	cmd := exec.Command(e.pythonPath, pipArgs...)

	fmt.Println("\nInstalling modules, please wait...")
	fmt.Printf("Executing command: %s %s\n", e.pythonPath, strings.Join(pipArgs, " "))
	fmt.Println("----------------------------------------")

	// Execute pip install command
	output, err := cmd.CombinedOutput()

	fmt.Println("----------------------------------------")

	if err != nil {
		fmt.Printf("Module installation failed: %v\n", err)
		fmt.Println("Please manually run the following command to install modules:")
		fmt.Printf("%s %s\n", e.pythonPath, strings.Join(pipArgs, " "))
		return false
	} else {
		fmt.Println("Modules installed successfully!")
		fmt.Println(string(output))
		return true
	}
}

// handleMissingModulesAndRetry handles missing modules and retries execution
func (e *PythonExecutor) handleMissingModulesAndRetry() int {
	// Detect missing modules
	missingModules := e.detectMissingModules(e.lastError)

	if len(missingModules) == 0 {
		// No missing modules detected, return original exit code
		return 1
	}

	// Try to install missing modules
	if e.installMissingModules(missingModules) {
		fmt.Println("\nRe-executing Python script...")

		// Re-execute script
		var exitCode int
		if e.captureOutput {
			exitCode = e.executeWithOutputCapture()
		} else {
			exitCode = e.executeWithTerminalHandling()
		}

		// If failed again, recursively handle
		if exitCode != 0 {
			return e.handleMissingModulesAndRetry()
		}

		return exitCode
	}

	return 1
}

// Helper function to check if a string slice contains a string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}