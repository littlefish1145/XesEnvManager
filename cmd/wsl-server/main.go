//go:build linux || unix
// +build linux unix

package main

import (
	"flag"
	"fmt"
	"log"

	"python-manager/pkg/wsl"
)

func main() {
	port := flag.String("port", "50051", "Port to listen on")
	flag.Parse()

	fmt.Printf("Starting WSL Python Service server on port %s...\n", *port)
	
	if err := wsl.StartWSLServer(*port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}