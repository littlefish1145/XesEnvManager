package wsl

import (
	"context"
	"fmt"

	// "log"
	"python-manager/pkg/model"
	"python-manager/proto"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// WSLClient connects to the WSL Python service
type WSLClient struct {
	conn   *grpc.ClientConn
	client proto.PythonServiceClient
}

// NewWSLClient creates a new WSL client
func NewWSLClient(address string) (*WSLClient, error) {
	// Set up gRPC connection with insecure credentials (for local communication)
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to WSL server: %v", err)
	}

	client := proto.NewPythonServiceClient(conn)

	return &WSLClient{
		conn:   conn,
		client: client,
	}, nil
}

// Close closes the gRPC connection
func (c *WSLClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SearchEnvironments searches for Python environments via WSL
func (c *WSLClient) SearchEnvironments() ([]model.PythonEnvironment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &proto.SearchEnvironmentsRequest{}
	resp, err := c.client.SearchEnvironments(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to search environments: %v", err)
	}

	// Convert proto environments to our internal PythonEnvironment type
	var environments []model.PythonEnvironment
	for _, env := range resp.Environments {
		pyEnv := model.PythonEnvironment{
			Name:     env.Name,
			Path:     env.Path,
			Version:  env.Version,
			Enabled:  env.Enabled,
			Encoding: env.Encoding,
			Platform: env.Platform,
		}
		environments = append(environments, pyEnv)
	}

	return environments, nil
}

// ExecutePython executes a Python script via WSL
func (c *WSLClient) ExecutePython(pythonPath string, arguments []string, captureOutput bool, autoExit bool, interactive bool) (int32, string, string, error) {
	return c.ExecutePythonWithInput(pythonPath, arguments, captureOutput, autoExit, interactive, "")
}

// ExecutePythonWithInput executes a Python script via WSL with input data
func (c *WSLClient) ExecutePythonWithInput(pythonPath string, arguments []string, captureOutput bool, autoExit bool, interactive bool, inputData string) (int32, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Convert Windows paths to WSL paths
	wslPythonPath := ConvertWindowsPathToWSL(pythonPath)
	wslArguments := make([]string, len(arguments))
	for i, arg := range arguments {
		// Only convert if it looks like a Windows path
		if strings.Contains(arg, "\\") || (len(arg) >= 2 && arg[1] == ':') {
			wslArguments[i] = ConvertWindowsPathToWSL(arg)
		} else {
			wslArguments[i] = arg
		}
	}

	req := &proto.ExecutePythonRequest{
		PythonPath:    wslPythonPath,
		Arguments:     wslArguments,
		CaptureOutput: captureOutput,
		AutoExit:      autoExit,
		Interactive:   interactive,
		InputData:     inputData,
	}

	resp, err := c.client.ExecutePython(ctx, req)
	if err != nil {
		return 1, "", fmt.Sprintf("failed to execute Python: %v", err), err
	}

	return resp.ExitCode, resp.Output, resp.Error, nil
}

// ConvertWindowsPathToWSL converts Windows paths to WSL paths
func ConvertWindowsPathToWSL(path string) string {
	// Convert Windows drive letters like C:\ to /mnt/c/
	if len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(string(path[0]))
		rest := path[2:]
		// Replace backslashes with forward slashes
		rest = strings.ReplaceAll(rest, "\\", "/")
		return "/mnt/" + drive + rest
	}

	// Replace backslashes with forward slashes for other cases
	return strings.ReplaceAll(path, "\\", "/")
}

// TestWSLConnection tests if we can connect to the WSL server
func TestWSLConnection(address string) bool {
	// Set up gRPC connection with a short timeout to avoid hanging
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Dial with block and timeout to quickly check if server is up
	conn, err := grpc.DialContext(ctx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)

	if err != nil {
		// Silently return false if connection fails - this is normal if WSL server is not running
		return false
	}
	defer conn.Close()

	return true
}
