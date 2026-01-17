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
	"net/http"
	_ "net/http/pprof"
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

// Pre-compiled regex patterns for performance
var (
	pythonVersionRegexOnce sync.Once
	pythonVersionRegex    *regexp.Regexp
	pythonVersionRegex2   *regexp.Regexp
)

// getPythonVersionRegex returns the compiled version regex patterns
func getPythonVersionRegex() (*regexp.Regexp, *regexp.Regexp) {
	pythonVersionRegexOnce.Do(func() {
		pythonVersionRegex = regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)
		pythonVersionRegex2 = regexp.MustCompile(`Python (\d+\.\d+\.\d+\S*)`)
	})
	return pythonVersionRegex, pythonVersionRegex2
}

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

	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		terminateProcess(cmd.Process)
	}

	closeCommandResources(cmd)
}

func terminateProcess(process *os.Process) {
	pgid, err := syscall.Getpgid(process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		process.Kill()
	}

	time.Sleep(100 * time.Millisecond)

	if processState := getProcessState(process); processState == nil || !processState.Exited() {
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			process.Kill()
		}
	}
}

func getProcessState(process *os.Process) *os.ProcessState {
	if process == nil {
		return nil
	}
	return process.ProcessState
}

func closeCommandResources(cmd *exec.Cmd) {
	if cmd.Stdout != nil {
		if closer, ok := cmd.Stdout.(io.Closer); ok {
			closer.Close()
		}
	}
	if cmd.Stderr != nil {
		if closer, ok := cmd.Stderr.(io.Closer); ok {
			closer.Close()
		}
	}
	if cmd.Stdin != nil {
		if closer, ok := cmd.Stdin.(io.Closer); ok {
			closer.Close()
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

	if err := validatePythonRequest(req); err != nil {
		return createErrorResponse(1, err.Error()), nil
	}

	cmd := createPythonCommand(req)
	setupProcessGroup(cmd)
	setupEnvironment(cmd)

	commandID := fmt.Sprintf("%d", time.Now().UnixNano())
	processManager.AddCommand(commandID, cmd)
	defer processManager.RemoveCommand(commandID)

	var response *proto.ExecutePythonResponse
	if req.Interactive {
		response = executeInteractive(ctx, cmd, req, commandID)
	} else if req.CaptureOutput {
		response = executeWithCapture(cmd)
	} else {
		response = executeWithoutCapture(cmd)
	}

	return response, nil
}

func validatePythonRequest(req *proto.ExecutePythonRequest) error {
	if req.PythonPath == "" {
		return fmt.Errorf("Python path is empty")
	}

	if _, err := os.Stat(req.PythonPath); os.IsNotExist(err) {
		return fmt.Errorf("Python executable does not exist: %s", req.PythonPath)
	}

	return nil
}

func createPythonCommand(req *proto.ExecutePythonRequest) *exec.Cmd {
	cmdArgs := []string{}
	cmdArgs = append(cmdArgs, req.Arguments...)
	return exec.Command(req.PythonPath, cmdArgs...)
}

func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func setupEnvironment(cmd *exec.Cmd) {
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONLEGACYWINDOWSSTDIO=utf-8",
	)
}

func createErrorResponse(exitCode int32, errorMsg string) *proto.ExecutePythonResponse {
	return &proto.ExecutePythonResponse{
		ExitCode: exitCode,
		Error:    errorMsg,
	}
}

func executeInteractive(ctx context.Context, cmd *exec.Cmd, req *proto.ExecutePythonRequest, commandID string) *proto.ExecutePythonResponse {
	stdinPipe, stdoutPipe, stderrPipe, err := createPipes(cmd)
	if err != nil {
		return createErrorResponse(1, err.Error())
	}

	if err := cmd.Start(); err != nil {
		closeAllPipes(stdinPipe, stdoutPipe, stderrPipe)
		return createErrorResponse(1, fmt.Sprintf("Failed to start command: %v", err))
	}

	stdinCtx, stdinCancel := context.WithCancel(context.Background())
	handleStdin(stdinCtx, stdinCancel, stdinPipe, req)

	stdoutBytes, stderrBytes, cmdErr := waitForInteractiveCommand(ctx, cmd, stdoutPipe, stderrPipe, stdinCancel, commandID)

	combinedOutput := string(stdoutBytes) + string(stderrBytes)
	return buildResponse(cmdErr, combinedOutput, string(stdoutBytes))
}

func createPipes(cmd *exec.Cmd) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed to create stdin pipe: %v", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return nil, nil, nil, fmt.Errorf("Failed to create stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		return nil, nil, nil, fmt.Errorf("Failed to create stderr pipe: %v", err)
	}

	return stdinPipe, stdoutPipe, stderrPipe, nil
}

func closeAllPipes(stdinPipe io.WriteCloser, stdoutPipe, stderrPipe io.ReadCloser) {
	stdinPipe.Close()
	stdoutPipe.Close()
	stderrPipe.Close()
}

func handleStdin(ctx context.Context, cancel context.CancelFunc, stdinPipe io.WriteCloser, req *proto.ExecutePythonRequest) {
	if req.InputData != "" {
		if _, err := stdinPipe.Write([]byte(req.InputData)); err != nil {
			log.Printf("Warning: Failed to write input data: %v", err)
		}
		stdinPipe.Close()
		cancel()
		return
	}

	go func() {
		defer func() {
			stdinPipe.Close()
			cancel()
		}()

		stat, err := os.Stdin.Stat()
		if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		io.Copy(stdinPipe, os.Stdin)
	}()
}

func waitForInteractiveCommand(ctx context.Context, cmd *exec.Cmd, stdoutPipe, stderrPipe io.ReadCloser, stdinCancel context.CancelFunc, commandID string) ([]byte, []byte, error) {
	stdoutChan := make(chan []byte, 1)
	stderrChan := make(chan []byte, 1)
	var wg sync.WaitGroup

	wg.Add(2)
	go readToChannel(stdoutPipe, stdoutChan, &wg, "stdout")
	go readToChannel(stderrPipe, stderrChan, &wg, "stderr")

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timeout := 30 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	var cmdErr error
	select {
	case cmdErr = <-done:
	case <-time.After(timeout):
		log.Printf("Command timed out, terminating...")
		stdinCancel()
		cmd.Process.Kill()
		<-done
		processManager.RemoveCommand(commandID)
		return nil, nil, fmt.Errorf("Command execution timed out")
	case <-ctx.Done():
		log.Printf("Context cancelled, terminating command...")
		stdinCancel()
		cmd.Process.Kill()
		<-done
		processManager.RemoveCommand(commandID)
		return nil, nil, fmt.Errorf("Command execution was cancelled")
	}

	wg.Wait()

	stdoutBytes := receiveFromChannel(stdoutChan, "stdout")
	stderrBytes := receiveFromChannel(stderrChan, "stderr")

	return stdoutBytes, stderrBytes, cmdErr
}

func readToChannel(pipe io.ReadCloser, ch chan []byte, wg *sync.WaitGroup, pipeName string) {
	defer wg.Done()
	data, err := ioutil.ReadAll(pipe)
	if err != nil {
		log.Printf("Error reading %s: %v", pipeName, err)
	}
	ch <- data
}

func receiveFromChannel(ch chan []byte, pipeName string) []byte {
	select {
	case data := <-ch:
		return data
	default:
		log.Printf("Warning: No %s data received", pipeName)
		return nil
	}
}

func executeWithCapture(cmd *exec.Cmd) *proto.ExecutePythonResponse {
	output, err := cmd.CombinedOutput()
	return buildResponse(err, string(output), string(output))
}

func executeWithoutCapture(cmd *exec.Cmd) *proto.ExecutePythonResponse {
	err := cmd.Run()
	return buildResponse(err, "", "")
}

func buildResponse(err error, combinedOutput, stdoutOutput string) *proto.ExecutePythonResponse {
	if err == nil {
		return &proto.ExecutePythonResponse{
			ExitCode: 0,
			Output:   combinedOutput,
			Error:    "",
		}
	}

	if exitError, ok := err.(*exec.ExitError); ok {
		return &proto.ExecutePythonResponse{
			ExitCode: int32(exitError.ExitCode()),
			Output:   combinedOutput,
			Error:    stdoutOutput,
		}
	}

	return &proto.ExecutePythonResponse{
		ExitCode: 1,
		Output:   combinedOutput,
		Error:    err.Error(),
	}
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

	if !isCondaAvailable() {
		return condaEnvs
	}

	condaEnvs = append(condaEnvs, getCondaEnvList()...)

	if len(condaEnvs) == 0 {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			condaEnvs = append(condaEnvs, searchMinicondaEnvs(homeDir)...)
			condaEnvs = append(condaEnvs, searchAnacondaEnvs(homeDir)...)
		}
	}

	return condaEnvs
}

func isCondaAvailable() bool {
	cmd := exec.Command("which", "conda")
	return cmd.Run() == nil
}

func getCondaEnvList() []string {
	var condaEnvs []string

	cmd := exec.Command("conda", "env", "list")
	output, err := cmd.Output()
	if err != nil {
		return condaEnvs
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if line == "*" || strings.HasPrefix(line, "*") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 2 {
			envName := parts[0]
			envPath := parts[1]

			if envName == "*" {
				continue
			}

			pythonPath := filepath.Join(strings.Trim(envPath, "* "), "bin", "python")
			if _, err := os.Stat(pythonPath); err == nil {
				condaEnvs = append(condaEnvs, pythonPath)
			}
		}
	}

	return condaEnvs
}

func searchMinicondaEnvs(homeDir string) []string {
	var condaEnvs []string

	minicondaPath := filepath.Join(homeDir, "miniconda3", "bin", "python")
	if _, err := os.Stat(minicondaPath); err == nil {
		condaEnvs = append(condaEnvs, minicondaPath)
	}

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

	return condaEnvs
}

func searchAnacondaEnvs(homeDir string) []string {
	var condaEnvs []string

	anacondaPath := filepath.Join(homeDir, "anaconda3", "bin", "python")
	if _, err := os.Stat(anacondaPath); err == nil {
		condaEnvs = append(condaEnvs, anacondaPath)
	}

	envsDir := filepath.Join(homeDir, "anaconda3", "envs")
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

	currentPath, _ := os.Getwd()
	currentPath = filepath.Clean(currentPath)

	venvEnvs = append(venvEnvs, searchVenvInPathTree(currentPath)...)

	homeDir, err := os.UserHomeDir()
	if err == nil {
		venvEnvs = append(venvEnvs, searchVenvInHome(homeDir)...)
	}

	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv != "" {
		pythonPath := filepath.Join(virtualEnv, "bin", "python")
		if _, err := os.Stat(pythonPath); err == nil {
			venvEnvs = append(venvEnvs, pythonPath)
		}
	}

	return venvEnvs
}

func searchVenvInPathTree(currentPath string) []string {
	var venvEnvs []string

	for i := 0; i < 5; i++ {
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

		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			break
		}
		currentPath = parentPath
	}

	return venvEnvs
}

func searchVenvInHome(homeDir string) []string {
	var venvEnvs []string

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
	pythonVersionRegex, pythonVersionRegex2 := getPythonVersionRegex()
	matches := pythonVersionRegex.FindStringSubmatch(versionOutput)
	if len(matches) > 1 {
		return matches[1]
	}

	// Try alternative format "Python 3.9.7+" (with possible revision)
	matches = pythonVersionRegex2.FindStringSubmatch(versionOutput)
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

	// Start pprof server if enabled
	go func() {
		if os.Getenv("PPROF_ENABLED") == "true" {
			pprofPort := "6060"
			if envPort := os.Getenv("PPROF_PORT"); envPort != "" {
				pprofPort = envPort
			}
			log.Printf("Starting pprof server on :%s", pprofPort)
			if err := http.ListenAndServe(":"+pprofPort, nil); err != nil {
				log.Printf("Pprof server failed: %v", err)
			}
		}
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
