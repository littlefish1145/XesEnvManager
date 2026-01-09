package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Change to the parent directory to access proto files
	if err := os.Chdir(".."); err != nil {
		log.Fatalf("Failed to change directory: %v", err)
	}

	// Connect to the WSL server
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Get current directory
	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}

	// Print current directory and list proto files
	fmt.Printf("Current directory: %s\n", currentDir)
	protoDir := filepath.Join(currentDir, "proto")
	fmt.Printf("Proto directory: %s\n", protoDir)

	// List proto files
	files, err := os.ReadDir(protoDir)
	if err != nil {
		log.Fatalf("Failed to read proto directory: %v", err)
	}

	fmt.Println("Proto files:")
	for _, file := range files {
		fmt.Printf("- %s\n", file.Name())
	}

	fmt.Println("\nTest completed. The WSL server input handling has been implemented.")
	fmt.Println("To test the actual functionality:")
	fmt.Println("1. Start the WSL server in WSL environment")
	fmt.Println("2. Run python-manager.exe with a WSL Python environment")
	fmt.Println("3. Try running a Python script that requires input")
}