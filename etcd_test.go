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
func setupEtcd(ctx context.Context, t *testing.T) (*EtcdRegistry, testcontainers.Container, func()) {
	etcdContainer, err := etcd.Run(ctx, "bitnami/etcd:3.5.20", testcontainers.WithEnv(map[string]string{
		"ALLOW_NONE_AUTHENTICATION": "yes",
		"ETCD_ADVERTISE_CLIENT_URLS": "http://0.0.0.0:2379",
		"ETCD_LISTEN_CLIENT_URLS":    "http://0.0.0.0:2379",
	}))
	if err != nil {
		t.Fatalf("failed to start etcd container: %s", err)
	}

	endpoint, err := etcdContainer.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("failed to get etcd endpoint: %s", err)
	}
	endpoints := []string{endpoint}

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

	return registry, etcdContainer, cleanup
}

func TestIntegration_RegisterAndGetService(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, _, cleanup := setupEtcd(ctx, t)
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

func TestIntegration_WatchService(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second) // Increased timeout for watch test
	defer cancel()

	registry, _, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	serviceName := "watch-test-service"

	// Channel to receive updates from the watch callback
	watchUpdates := make(chan []*ServiceInfo, 5) // Buffer to avoid blocking

	// Start watching the service
	err := registry.WatchService(ctx, serviceName, func(services []*ServiceInfo) {
		watchUpdates <- services
	})
	assert.NoError(t, err)

	// --- Test Case 1: Initial state (no services) ---
	select {
	case services := <-watchUpdates:
		assert.Empty(t, services, "Expected no services initially")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for initial watch update")
	}

	// --- Test Case 2: Register a service ---
	service1 := &ServiceInfo{
		Name:    serviceName,
		ID:      "watch-instance-1",
		Address: "127.0.0.1",
		Port:    "8001",
	}
	err = registry.Register(ctx, service1)
	assert.NoError(t, err)

	select {
	case services := <-watchUpdates:
		assert.Len(t, services, 1, "Expected 1 service after registration")
		assert.Equal(t, service1.ID, services[0].ID, "Expected service1 to be present")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for watch update after first registration")
	}

	// --- Test Case 3: Register another service ---
	service2 := &ServiceInfo{
		Name:    serviceName,
		ID:      "watch-instance-2",
		Address: "127.0.0.1",
		Port:    "8002",
	}
	// Create a new registry for service2 to simulate a separate process
	registry2, _, cleanup2 := setupEtcd(ctx, t)
	defer cleanup2()

	err = registry2.Register(ctx, service2)
	assert.NoError(t, err)

	select {
	case services := <-watchUpdates:
		assert.Len(t, services, 2, "Expected 2 services after second registration")
		// Check if both services are present (order might vary)
		found1, found2 := false, false
		for _, s := range services {
			if s.ID == service1.ID {
				found1 = true
			}
			if s.ID == service2.ID {
				found2 = true
			}
		}
		assert.True(t, found1 && found2, "Expected both service1 and service2 to be present")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for watch update after second registration")
	}

	// --- Test Case 4: Deregister a service ---
	err = registry.Deregister()
	assert.NoError(t, err)

	select {
	case services := <-watchUpdates:
		assert.Len(t, services, 1, "Expected 1 service after deregistration")
		assert.Equal(t, service2.ID, services[0].ID, "Expected only service2 to remain")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for watch update after deregistration")
	}

	// --- Test Case 5: Deregister the last service ---
	err = registry2.Deregister()
	assert.NoError(t, err)

	select {
	case services := <-watchUpdates:
		assert.Empty(t, services, "Expected no services after last deregistration")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for watch update after last deregistration")
	}

	// Give some time for watch goroutine to process final events before context cancellation
	time.Sleep(1 * time.Second)
}

func TestIntegration_AutoReRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Increased timeout for re-register test
	defer cancel()

	registry, etcdContainer, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	serviceInfo := &ServiceInfo{
		Name:    "re-register-service",
		ID:      "re-register-instance-1",
		Address: "localhost",
		Port:    "8080",
	}

	// 1. Register the service
	err := registry.Register(ctx, serviceInfo)
	assert.NoError(t, err)

	// Verify it's registered initially
	services, err := registry.GetService(ctx, serviceInfo.Name)
	assert.NoError(t, err)
	assert.Len(t, services, 1, "Expected service to be registered initially")

	// 2. Stop the etcd container to simulate downtime
	t.Log("Stopping etcd container to simulate downtime...")
	err = etcdContainer.Stop(ctx, nil) // nil for default stop timeout
	assert.NoError(t, err)

	// Wait for longer than TTL to ensure lease expires
	t.Logf("Waiting for %d seconds (2x TTL) for lease to expire...", registry.opts.TTL*2)
	time.Sleep(time.Duration(registry.opts.TTL*2) * time.Second)

	// 3. Start the etcd container to simulate recovery
	t.Log("Starting etcd container to simulate recovery...")
	err = etcdContainer.Start(ctx)
	assert.NoError(t, err)

	// 4. Wait for re-registration to occur
	t.Log("Waiting for service to re-register...")
	assert.Eventually(t, func() bool {
		services, err := registry.GetService(ctx, serviceInfo.Name)
		return err == nil && len(services) == 1 && services[0].ID == serviceInfo.ID
	}, 15*time.Second, 1*time.Second, "Service did not re-register within expected time")

	t.Log("Service successfully re-registered.")
}

func TestIntegration_DeleteServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, _, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	// --- Setup: Register multiple services ---
	service1 := &ServiceInfo{Name: "app-service", ID: "app-1", Address: "1.1.1.1", Port: "8080"}
	service2 := &ServiceInfo{Name: "app-service", ID: "app-2", Address: "1.1.1.2", Port: "8080"}
	service3 := &ServiceInfo{Name: "web-service", ID: "web-1", Address: "2.2.2.1", Port: "8080"}
	service4 := &ServiceInfo{Name: "web-service", ID: "web-2", Address: "2.2.2.2", Port: "8080"}
	service5 := &ServiceInfo{Name: "other-service", ID: "other-1", Address: "3.3.3.1", Port: "8080"}

	for _, s := range []*ServiceInfo{service1, service2, service3, service4, service5} {
		err := registry.Register(ctx, s)
		assert.NoError(t, err)
	}

	// Verify all are registered
	services, err := registry.GetService(ctx, "app-service")
	assert.NoError(t, err)
	assert.Len(t, services, 2)
	services, err = registry.GetService(ctx, "web-service")
	assert.NoError(t, err)
	assert.Len(t, services, 2)
	services, err = registry.GetService(ctx, "other-service")
	assert.NoError(t, err)
	assert.Len(t, services, 1)

	// --- Test DeleteServiceKey ---
	t.Run("Delete single service key", func(t *testing.T) {
		// Delete app-1
		err := registry.DeleteServiceKey(ctx, service1.Name, service1.ID)
		assert.NoError(t, err)

		// Verify app-1 is gone, app-2 remains
		services, err = registry.GetService(ctx, "app-service")
		assert.NoError(t, err)
		assert.Len(t, services, 1)
		assert.Equal(t, service2.ID, services[0].ID)

		// Try deleting non-existent key
		err = registry.DeleteServiceKey(ctx, "non-existent", "id")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "service key does not exist")
	})

	// --- Test DeleteServiceByPrefix ---
	t.Run("Delete services by prefix", func(t *testing.T) {
		// Delete all web-services
		deletedCount, err := registry.DeleteServiceByPrefix(ctx, "web-service")
		assert.NoError(t, err)
		assert.Equal(t, int64(2), deletedCount)

		// Verify web-services are gone
		services, err = registry.GetService(ctx, "web-service")
		assert.NoError(t, err)
		assert.Len(t, services, 0)

		// Verify other services are untouched
		services, err = registry.GetService(ctx, "other-service")
		assert.NoError(t, err)
		assert.Len(t, services, 1)

		// Try deleting non-existent prefix
		deletedCount, err = registry.DeleteServiceByPrefix(ctx, "non-existent-prefix")
		assert.NoError(t, err) // No error for non-existent prefix, just 0 deleted
		assert.Equal(t, int64(0), deletedCount)
	})
}

func TestIntegration_ErrorScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	t.Run("NewEtcdRegistry with invalid endpoint", func(t *testing.T) {
		// Use a non-routable IP address and a random port
		invalidEndpoints := []string{"192.0.2.1:12345"}
		logger := log.New(os.Stdout, "[ETCD-TEST-ERROR] ", log.LstdFlags)

		_, err := NewEtcdRegistry(invalidEndpoints, WithLogger(logger), WithDialTimeout(1*time.Second))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to connect to etcd")
	})
}

func TestUnit_EdgeCases(t *testing.T) {
	t.Parallel()

	// This test covers edge cases that don't require a running etcd instance.
	registry := &EtcdRegistry{
		opts:   Options{TTL: 5},
		logger: &discardLogger{},
		// client is intentionally left nil
	}

	t.Run("Register with nil service info", func(t *testing.T) {
		err := registry.Register(context.Background(), nil)
		assert.Error(t, err)
		assert.Equal(t, "service info cannot be nil", err.Error())
	})

	t.Run("Deregister when not registered", func(t *testing.T) {
		// Should be a no-op and not panic or return an error.
		err := registry.Deregister()
		assert.NoError(t, err)
	})

	t.Run("DeleteServiceKey with uninitialized client", func(t *testing.T) {
		err := registry.DeleteServiceKey(context.Background(), "any-service", "any-id")
		assert.Error(t, err)
		assert.Equal(t, "etcd client is not initialized", err.Error())
	})

	t.Run("DeleteServiceByPrefix with uninitialized client", func(t *testing.T) {
		_, err := registry.DeleteServiceByPrefix(context.Background(), "any-prefix")
		assert.Error(t, err)
		assert.Equal(t, "etcd client is not initialized", err.Error())
	})
}

func TestIntegration_WatchWithContextCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	registry, _, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	serviceName := "watch-cancel-test"

	// Create a context that we can cancel manually.
	watchCtx, watchCancel := context.WithCancel(context.Background())

	watchEnded := make(chan struct{})
	go func() {
		// This callback should not be called if no service is registered.
		callback := func(services []*ServiceInfo) {
			t.Logf("Watch callback unexpectedly called with %d services", len(services))
		}
		// WatchService will block until the context is cancelled.
		_ = registry.WatchService(watchCtx, serviceName, callback)
		// Signal that the watch has ended.
		close(watchEnded)
	}()

	// Give the watch a moment to start.
	time.Sleep(1 * time.Second)

	// Cancel the context to stop the watch.
	t.Log("Cancelling watch context...")
	watchCancel()

	// Wait for the watch goroutine to exit.
	select {
	case <-watchEnded:
		// This is the expected outcome.
		t.Log("Watch goroutine exited gracefully.")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for watch goroutine to exit after context cancellation")
	}
}
