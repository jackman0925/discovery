# Etcd Service Discovery for Go

A robust and easy-to-use Go library for service registration and discovery using etcd v3.

This library provides a clean, flexible, and resilient way to manage microservice lifecycle and discovery within an etcd cluster.

## Features

- **Clean API**: Uses the Functional Options Pattern for clear and extensible configuration.
- **Automatic Keep-Alive**: Automatically manages service liveness using etcd's lease and keep-alive features.
- **Robust Lifecycle Management**: Clear separation of `Register`, `Deregister`, and `Close` methods for predictable behavior.
- **Background Re-registration**: Automatically attempts to re-register the service if the connection or lease is lost.
- **Pluggable Logger**: Integrate your own logging solution by implementing a simple `Logger` interface.
- **Service Discovery**: Find and get a list of active service instances.
- **Service Watching**: Watch for real-time changes in service instances (new instances, crashes, etc.).

## Installation

```sh
go get github.com/your-username/discovery
```
*(Please replace `github.com/your-username/discovery` with your actual repository path)*

## Usage

Here is a complete example of how to register a service and properly shut it down.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/your-username/discovery"
)

func main() {
	// Use the standard logger for this example
	logger := log.New(os.Stdout, "[DISCOVERY-EXAMPLE] ", log.LstdFlags)

	// Create a new registry instance
	registry, err := discovery.NewEtcdRegistry(
		[]string{"localhost:2379"}, // Etcd server endpoints
		discovery.WithTTL(15),             // Set lease TTL to 15 seconds
		discovery.WithNamespace("prod"),   // Set a namespace for services
		discovery.WithLogger(logger),      // Provide a logger
	)
	if err != nil {
		log.Fatalf("Failed to create etcd registry: %v", err)
	}

	// Ensure the registry is closed on exit
	defer registry.Close()

	// Define the service to be registered
	serviceInfo := &discovery.ServiceInfo{
		Name:    "my-awesome-api",
		ID:      "instance-01",
		Address: "192.168.1.100",
		Port:    "8080",
		Version: "1.0.2",
	}

	// Register the service
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := registry.Register(ctx, serviceInfo); err != nil {
		log.Fatalf("Failed to register service: %v", err)
	}

	fmt.Println("Service registered successfully. It will be kept alive automatically.")

	// Keep the application running to simulate a live service
	// In a real application, this would be your main application logic (e.g., an HTTP server)
	select {
	case <-time.After(60 * time.Second):
		fmt.Println("Shutting down after 60 seconds...")
	}
}
```

## Configuration Options

The `NewEtcdRegistry` function accepts the following `Option` functions for configuration:

| Function              | Description                                               |
| --------------------- | --------------------------------------------------------- |
| `WithNamespace(string)` | Sets a namespace to isolate services (default: `default`). |
| `WithTTL(int64)`        | Sets the service lease Time-To-Live in seconds (default: `30`). |
| `WithAuth(user, pass)`  | Sets the username and password for etcd authentication.     |
| `WithKeyPrefix(string)` | Sets the root prefix for all keys (default: `/etcd_registry`). |
| `WithLogger(Logger)`    | Provides a custom logger that implements the `Logger` interface. |
| `WithDialTimeout(d)`    | Sets the dial timeout for the etcd client.                |