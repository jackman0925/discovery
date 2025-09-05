# Etcd Service Discovery for Go

[![Go Report Card](https://goreportcard.com/badge/github.com/jackman0925/discovery)](https://goreportcard.com/report/github.com/jackman0925/discovery)
[![Go Reference](https://pkg.go.dev/badge/github.com/jackman0925/discovery.svg)](https://pkg.go.dev/github.com/jackman0925/discovery)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

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
- **Leader Election**: Elect a single leader among a group of service instances to perform special tasks.

## Leader Election

In addition to service discovery, the library supports leader election. This is useful in distributed systems where you need to ensure that only one instance of a service is performing a specific task at any given time (e.g., running a cron job, processing a queue, etc.).

The election is built on top of the same etcd client and session, making it efficient and easy to use.

### Usage Example

Here is an example of how two service instances can campaign for leadership. One will win, and the other will block until the leader steps down.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jackman0925/discovery"
)

func main() {
	logger := log.New(os.Stdout, "[ELECTION-EXAMPLE] ", log.LstdFlags)
	etcdEndpoints := []string{"localhost:2379"}

	// Create two separate registry instances to simulate two different nodes
	reg1, err := discovery.NewEtcdRegistry(etcdEndpoints, discovery.WithLogger(logger))
	if err != nil {
		log.Fatalf("Failed to create registry 1: %v", err)
	}
	defer reg1.Close()

	reg2, err := discovery.NewEtcdRegistry(etcdEndpoints, discovery.WithLogger(logger))
	if err != nil {
		log.Fatalf("Failed to create registry 2: %v", err)
	}
	defer reg2.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// --- Candidate 1 ---
	go func() {
		defer wg.Done()
		election, err := reg1.NewElection(discovery.ElectionOptions{
			ElectionName: "my-critical-task",
			Proposal:     "candidate-1",
		})
		if err != nil {
			logger.Printf("candidate-1: failed to create election: %v", err)
			return
		}

		// Campaign for leadership. This call blocks until leadership is won or the context is cancelled.
		if err := election.Campaign(context.Background()); err != nil {
			logger.Printf("candidate-1: campaign failed: %v", err)
			return
		}

		logger.Println("candidate-1: I am the leader!")

		// Hold leadership for 10 seconds, then resign.
		time.Sleep(10 * time.Second)

		logger.Println("candidate-1: Resigning from leadership.")
		if err := election.Resign(context.Background()); err != nil {
			logger.Printf("candidate-1: failed to resign: %v", err)
		}
	}()

	// --- Candidate 2 ---
	go func() {
		defer wg.Done()
		// Give candidate 1 a head start
		time.Sleep(1 * time.Second)

		election, err := reg2.NewElection(discovery.ElectionOptions{
			ElectionName: "my-critical-task",
			Proposal:     "candidate-2",
		})
		if err != nil {
			logger.Printf("candidate-2: failed to create election: %v", err)
			return
		}

		logger.Println("candidate-2: Campaigning for leadership...")
		if err := election.Campaign(context.Background()); err != nil {
			logger.Printf("candidate-2: campaign failed: %v", err)
			return
		}

		// This part will only execute after candidate-1 has resigned.
		logger.Println("candidate-2: I am the leader now!")
		time.Sleep(5 * time.Second)
		logger.Println("candidate-2: Resigning from leadership.")
		if err := election.Resign(context.Background()); err != nil {
			logger.Printf("candidate-2: failed to resign: %v", err)
		}
	}()

	wg.Wait()
	logger.Println("Example finished.")
}

```

## Installation

```sh
go get github.com/jackman0925/discovery
```

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

	"github.com/jackman0925/discovery"
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

## Testing

The library has a comprehensive test suite that includes both unit and integration tests.

- **Unit Tests**: These tests do not have any external dependencies and can be run easily.
- **Integration Tests**: These tests require a running Docker environment to spin up an etcd container.

To run all tests:
```sh
go test -v ./...
```

To run only the unit tests (which is faster and doesn't require Docker):
```sh
go test -v -short ./...
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
