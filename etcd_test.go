package discovery

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/etcd"
)

func TestGetServiceKey(t *testing.T) {
	t.Parallel()

	registry := &EtcdRegistry{
		opts: Options{
			KeyPrefix: "/test_prefix",
			Namespace: "test_namespace",
		},
		logger: &discardLogger{},
	}

	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{
			name:     "my-service",
			id:       "instance-1",
			expected: "/test_prefix/test_namespace/services/my-service/instance-1",
		},
		{
			name:     "another-service",
			id:       "node-abc-123",
			expected: "/test_prefix/test_namespace/services/another-service/node-abc-123",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual := registry.getServiceKey(tt.name, tt.id)
			if actual != tt.expected {
				t.Errorf("expected key '%s', but got '%s'", tt.expected, actual)
			}
		})
	}
}

// setupEtcd is a helper function to set up an etcd container for tests.
func setupEtcd(ctx context.Context, t *testing.T) (*EtcdRegistry, func()) {
	etcdContainer, err := etcd.RunContainer(ctx, testcontainers.WithImage("etcd:v3.5.12"))
	if err != nil {
		t.Fatalf("failed to start etcd container: %s", err)
	}

	endpoints, err := etcdContainer.Endpoints(ctx)
	if err != nil {
		t.Fatalf("failed to get etcd endpoints: %s", err)
	}

	// Use the standard log library for test output for easier debugging.
	logger := log.New(os.Stdout, "[ETCD-TEST] ", log.LstdFlags)

	registry, err := NewEtcdRegistry(endpoints, WithLogger(logger), WithTTL(5)) // Use a short TTL for testing
	if err != nil {
		t.Fatalf("failed to create etcd registry: %s", err)
	}

	// Teardown function to clean up resources
	cleanup := func() {
		if err := registry.Close(); err != nil {
			t.Logf("error closing registry: %s", err)
		}
		if err := etcdContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate etcd container: %s", err)
		}
	}

	return registry, cleanup
}

func TestIntegration_RegisterAndGetService(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	serviceInfo := &ServiceInfo{
		Name:    "test-service",
		ID:      "test-instance-1",
		Address: "localhost",
		Port:    "8080",
	}

	// 1. Register the service
	err := registry.Register(ctx, serviceInfo)
	assert.NoError(t, err)

	// 2. Get the service
	services, err := registry.GetService(ctx, "test-service")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(services))
	assert.Equal(t, "test-instance-1", services[0].ID)
	assert.Equal(t, "localhost", services[0].Address)

	// 3. Deregister the service
	err = registry.Deregister()
	assert.NoError(t, err)

	// 4. Verify the service is gone
	services, err = registry.GetService(ctx, "test-service")
	assert.NoError(t, err)
	assert.Equal(t, 0, len(services))
}