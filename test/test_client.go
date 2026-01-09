package main

import (
	"context"
	"fmt"
	"log"
	"time"

	proto "python-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Connect to the WSL server
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := proto.NewPythonServiceClient(conn)

	// Test 1: Search for Python environments
	fmt.Println("=== Testing SearchEnvironments ===")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	searchResp, err := client.SearchEnvironments(ctx, &proto.SearchEnvironmentsRequest{})
	if err != nil {
		log.Fatalf("SearchEnvironments failed: %v", err)
	}

	fmt.Printf("Found %d Python environments:\n", len(searchResp.Environments))
	for _, env := range searchResp.Environments {
		fmt.Printf("- Name: %s, Path: %s, Version: %s, Enabled: %t\n", 
			env.Name, env.Path, env.Version, env.Enabled)
	}

	// Test 2: Execute Python script with output capture
	fmt.Println("\n=== Testing ExecutePython with output capture ===")
	if len(searchResp.Environments) > 0 {
		pythonPath := searchResp.Environments[0].Path
		
		// Test non-interactive execution
		execResp, err := client.ExecutePython(ctx, &proto.ExecutePythonRequest{
			PythonPath:   pythonPath,
			Arguments:    []string{"test_output.py"},
			CaptureOutput: true,
			Interactive:  false,
		})
		
		if err != nil {
			log.Fatalf("ExecutePython failed: %v", err)
		}
		
		fmt.Printf("Exit Code: %d\n", execResp.ExitCode)
		fmt.Printf("Output:\n%s\n", execResp.Output)
		if execResp.Error != "" {
			fmt.Printf("Error: %s\n", execResp.Error)
		}
		
		// Test interactive execution
		fmt.Println("\n=== Testing ExecutePython in interactive mode ===")
		execResp2, err := client.ExecutePython(ctx, &proto.ExecutePythonRequest{
			PythonPath:   pythonPath,
			Arguments:    []string{"test_output.py"},
			CaptureOutput: true,
			Interactive:  true,
		})
		
		if err != nil {
			log.Fatalf("ExecutePython (interactive) failed: %v", err)
		}
		
		fmt.Printf("Exit Code: %d\n", execResp2.ExitCode)
		fmt.Printf("Output:\n%s\n", execResp2.Output)
		if execResp2.Error != "" {
			fmt.Printf("Error: %s\n", execResp2.Error)
		}
	}
}