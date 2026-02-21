package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// LogLevel represents the level of logging.
type LogLevel int

const (
	// LogLevelError logs only errors.
	LogLevelError LogLevel = iota
	// LogLevelWarn logs warnings and errors.
	LogLevelWarn
	// LogLevelInfo logs info, warnings, and errors.
	LogLevelInfo
	// LogLevelDebug logs all messages including debug.
	LogLevelDebug
)

// Logger defines the interface for logging. This allows users to use their own logger.
type Logger interface {
	Errorf(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Debugf(format string, v ...interface{})
}

// discardLogger is a logger that outputs nothing.
type discardLogger struct{}

func (dl *discardLogger) Errorf(format string, v ...interface{}) {}
func (dl *discardLogger) Warnf(format string, v ...interface{})  {}
func (dl *discardLogger) Infof(format string, v ...interface{})  {}
func (dl *discardLogger) Debugf(format string, v ...interface{}) {}

// leveledLogger wraps a standard logger and implements the Logger interface with level control.
type leveledLogger struct {
	logger interface {
		Printf(format string, v ...interface{})
	}
	level LogLevel
}

// Errorf logs an error message if the current level allows it.
func (l *leveledLogger) Errorf(format string, v ...interface{}) {
	if l.level >= LogLevelError {
		l.logger.Printf("[ERROR] "+format, v...)
	}
}

// Warnf logs a warning message if the current level allows it.
func (l *leveledLogger) Warnf(format string, v ...interface{}) {
	if l.level >= LogLevelWarn {
		l.logger.Printf("[WARN] "+format, v...)
	}
}

// Infof logs an info message if the current level allows it.
func (l *leveledLogger) Infof(format string, v ...interface{}) {
	if l.level >= LogLevelInfo {
		l.logger.Printf("[INFO] "+format, v...)
	}
}

// Debugf logs a debug message if the current level allows it.
func (l *leveledLogger) Debugf(format string, v ...interface{}) {
	if l.level >= LogLevelDebug {
		l.logger.Printf("[DEBUG] "+format, v...)
	}
}

// ServiceInfo holds information about a registered service.
type ServiceInfo struct {
	Name     string            `json:"name"`
	ID       string            `json:"id"`
	Address  string            `json:"address"`
	Port     string            `json:"port"`
	Version  string            `json:"version"`
	Weight   int               `json:"weight"`
	Metadata map[string]string `json:"metadata"`
}

// Options holds configuration for the EtcdRegistry.
type Options struct {
	Namespace         string
	TTL               int64
	Username          string
	Password          string
	DialTimeout       time.Duration
	DialKeepAliveTime time.Duration
	KeyPrefix         string
	Logger            Logger
	LogLevel          LogLevel
}

// Option configures an EtcdRegistry.
type Option func(*Options)

// WithNamespace sets the namespace for service discovery.
func WithNamespace(namespace string) Option {
	return func(o *Options) {
		o.Namespace = namespace
	}
}

// WithTTL sets the lease time-to-live in seconds.
func WithTTL(ttl int64) Option {
	return func(o *Options) {
		o.TTL = ttl
	}
}

// WithAuth sets the username and password for etcd authentication.
func WithAuth(username, password string) Option {
	return func(o *Options) {
		o.Username = username
		o.Password = password
	}
}

// WithDialTimeout sets the dial timeout for the etcd client.
func WithDialTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.DialTimeout = timeout
	}
}

// WithKeyPrefix sets the root prefix for all keys.
func WithKeyPrefix(prefix string) Option {
	return func(o *Options) {
		o.KeyPrefix = prefix
	}
}

// WithLogger sets the logger for the registry.
func WithLogger(logger Logger) Option {
	return func(o *Options) {
		o.Logger = logger
	}
}

// WithLogLevel sets the log level for the registry.
func WithLogLevel(level LogLevel) Option {
	return func(o *Options) {
		o.LogLevel = level
	}
}

// WithLoggerAndLevel sets both the logger and log level for the registry.
// This is a convenience function that wraps the provided logger with a leveled logger.
func WithLoggerAndLevel(logger interface {
	Printf(format string, v ...interface{})
}, level LogLevel) Option {
	return func(o *Options) {
		o.Logger = &leveledLogger{
			logger: logger,
			level:  level,
		}
		o.LogLevel = level
	}
}

// EtcdRegistry provides service registration and discovery using etcd.
type EtcdRegistry struct {
	client      *clientv3.Client
	leaseID     clientv3.LeaseID
	serviceInfo *ServiceInfo
	stopSignal  chan struct{}
	wg          sync.WaitGroup
	opts        Options
	logger      Logger
	closeOnce   sync.Once
}

// NewEtcdRegistry creates a new EtcdRegistry instance.
func NewEtcdRegistry(endpoints []string, opts ...Option) (*EtcdRegistry, error) {
	options := Options{
		Namespace:         "default",
		TTL:               30,
		DialTimeout:       5 * time.Second,
		DialKeepAliveTime: 10 * time.Second,
		KeyPrefix:         "/etcd_registry",
		Logger:            &discardLogger{}, // Default to a silent logger
		LogLevel:          LogLevelInfo,     // Default to info level
	}

	for _, o := range opts {
		o(&options)
	}

	if options.TTL < 10 {
		options.Logger.Warnf("TTL(%d) is very low. It is recommended to set it to 10 seconds or more.", options.TTL)
	}

	config := clientv3.Config{
		Endpoints:         endpoints,
		DialTimeout:       options.DialTimeout,
		DialKeepAliveTime: options.DialKeepAliveTime,
		Username:          options.Username,
		Password:          options.Password,
	}

	options.Logger.Infof("Attempting to connect to etcd servers: %v", endpoints)
	client, err := clientv3.New(config)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create etcd client: %v, endpoints: %v", err, endpoints)
		options.Logger.Errorf("%s", errMsg)
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	// Verify the connection.
	ctx, cancel := context.WithTimeout(context.Background(), options.DialTimeout)
	defer cancel()
	if _, err = client.Status(ctx, endpoints[0]); err != nil {
		client.Close()
		errMsg := fmt.Sprintf("failed to connect to etcd: %v, endpoints: %v", err, endpoints)
		options.Logger.Errorf("%s", errMsg)
		return nil, fmt.Errorf(errMsg)
	}
	options.Logger.Infof("Successfully connected to etcd server.")

	return &EtcdRegistry{
		client:     client,
		stopSignal: make(chan struct{}),
		opts:       options,
		logger:     options.Logger,
	}, nil
}

// getServiceKey generates the full etcd key for a service.
func (e *EtcdRegistry) getServiceKey(name, id string) string {
	return fmt.Sprintf("%s/%s/services/%s/%s", e.opts.KeyPrefix, e.opts.Namespace, name, id)
}

// Register registers a service with etcd.
func (e *EtcdRegistry) Register(ctx context.Context, info *ServiceInfo) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	if info == nil {
		return fmt.Errorf("service info cannot be nil")
	}

	e.serviceInfo = info

	e.logger.Infof("Preparing to register service: %s (ID: %s)", info.Name, info.ID)

	data, err := json.Marshal(info)
	if err != nil {
		e.logger.Errorf("Failed to serialize service info: %v", err)
		return fmt.Errorf("failed to serialize service info: %w", err)
	}

	e.logger.Infof("Creating etcd lease, TTL=%d seconds", e.opts.TTL)
	lease, err := e.client.Grant(ctx, e.opts.TTL)
	if err != nil {
		e.logger.Errorf("Failed to create etcd lease: %v", err)
		return fmt.Errorf("failed to create etcd lease: %w", err)
	}
	e.leaseID = lease.ID
	e.logger.Infof("Successfully created lease, ID=%d", lease.ID)

	key := e.getServiceKey(info.Name, info.ID)
	e.logger.Infof("Registering service with key: %s", key)
	_, err = e.client.Put(ctx, key, string(data), clientv3.WithLease(lease.ID))
	if err != nil {
		e.logger.Errorf("Failed to register service: %v, key: %s", err, key)
		return fmt.Errorf("failed to register service: %w", err)
	}
	e.logger.Infof("Successfully wrote service registration.")

	e.wg.Add(1)
	go e.keepAlive()

	e.logger.Infof("Service [%s] successfully registered to etcd. Node ID: %s, Address: %s:%s",
		info.Name, info.ID, info.Address, info.Port)
	return nil
}

// keepAlive maintains the service lease.
func (e *EtcdRegistry) keepAlive() {
	defer e.wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.logger.Infof("Starting keep-alive for service [%s] (LeaseID: %d)", e.serviceInfo.Name, e.leaseID)

	keepAliveChan, err := e.client.KeepAlive(ctx, e.leaseID)
	if err != nil {
		e.logger.Errorf("Failed to create keep-alive: %v. Attempting to re-register...", err)
		e.reRegister()
		return
	}

	for {
		select {
		case <-e.stopSignal:
			e.logger.Infof("Received stop signal, stopping keep-alive for service [%s]", e.serviceInfo.Name)
			return
		case resp, ok := <-keepAliveChan:
			if !ok {
				e.logger.Warnf("Keep-alive channel closed for service [%s]. It may have expired. Attempting to re-register...", e.serviceInfo.Name)
				e.reRegister()
				return
			}
			e.logger.Debugf("Keep-alive for service [%s] is healthy. New TTL: %d", e.serviceInfo.Name, resp.TTL)
		}
	}
}

// reRegister attempts to re-register the service in the background.
func (e *EtcdRegistry) reRegister() {
	e.logger.Infof("Preparing to re-register service [%s] in the background", e.serviceInfo.Name)
	go func() {
		for {
			select {
			case <-e.stopSignal:
				e.logger.Infof("Stopping re-registration for service [%s]", e.serviceInfo.Name)
				return
			default:
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(e.opts.TTL)*time.Second)
			err := e.Register(ctx, e.serviceInfo)
			cancel()
			if err == nil {
				e.logger.Infof("Service [%s] re-registered successfully", e.serviceInfo.Name)
				return
			}

			e.logger.Errorf("Failed to re-register service [%s], will retry in 5 seconds...", e.serviceInfo.Name)
			time.Sleep(5 * time.Second)
		}
	}()
}

// Deregister unregisters the service from etcd.
func (e *EtcdRegistry) Deregister() error {
	var err error
	e.closeOnce.Do(func() {
		if e.leaseID == 0 || e.serviceInfo == nil {
			return
		}
		close(e.stopSignal)
		e.wg.Wait()
		e.logger.Infof("Revoking lease (ID: %d) for service [%s]", e.leaseID, e.serviceInfo.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = e.client.Revoke(ctx, e.leaseID)
		if err != nil {
			e.logger.Errorf("Failed to revoke lease: %v", err)
			err = fmt.Errorf("failed to revoke lease: %w", err)
			return
		}
		e.logger.Infof("Service [%s] successfully deregistered from etcd", e.serviceInfo.Name)
	})
	return err
}

// Close safely shuts down the registry.
func (e *EtcdRegistry) Close() error {
	if err := e.Deregister(); err != nil {
		e.logger.Warnf("An error occurred during deregistration: %v", err)
	}

	if e.client != nil {
		if err := e.client.Close(); err != nil {
			return fmt.Errorf("failed to close etcd client: %w", err)
		}
	}

	e.logger.Infof("Etcd client successfully closed.")
	return nil
}

// GetService retrieves all instances of a specific service.
func (e *EtcdRegistry) GetService(ctx context.Context, name string) ([]*ServiceInfo, error) {
	if e.client == nil {
		return nil, fmt.Errorf("etcd client is not initialized")
	}

	keyPrefix := fmt.Sprintf("%s/%s/services/%s/", e.opts.KeyPrefix, e.opts.Namespace, name)
	e.logger.Infof("Getting service list with key prefix: %s", keyPrefix)
	resp, err := e.client.Get(ctx, keyPrefix, clientv3.WithPrefix())
	if err != nil {
		e.logger.Errorf("Failed to get service list: %v", err)
		return nil, fmt.Errorf("failed to get service list: %w", err)
	}

	e.logger.Infof("Found %d service instances", len(resp.Kvs))
	services := make([]*ServiceInfo, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var service ServiceInfo
		if err := json.Unmarshal(kv.Value, &service); err != nil {
			e.logger.Errorf("Failed to parse service info: %v, key: %s", err, string(kv.Key))
			continue
		}
		services = append(services, &service)
	}

	return services, nil
}

// GetNamespaceServices retrieves all service instances in the current namespace.
func (e *EtcdRegistry) GetNamespaceServices(ctx context.Context) ([]*ServiceInfo, error) {
	if e.client == nil {
		return nil, fmt.Errorf("etcd client is not initialized")
	}

	keyPrefix := fmt.Sprintf("%s/%s/services/", e.opts.KeyPrefix, e.opts.Namespace)
	e.logger.Infof("Getting namespace service list with key prefix: %s", keyPrefix)
	resp, err := e.client.Get(ctx, keyPrefix, clientv3.WithPrefix())
	if err != nil {
		e.logger.Errorf("Failed to get namespace service list: %v", err)
		return nil, fmt.Errorf("failed to get namespace service list: %w", err)
	}

	e.logger.Infof("Found %d service instances in namespace [%s]", len(resp.Kvs), e.opts.Namespace)
	services := make([]*ServiceInfo, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var service ServiceInfo
		if err := json.Unmarshal(kv.Value, &service); err != nil {
			e.logger.Errorf("Failed to parse service info: %v, key: %s", err, string(kv.Key))
			continue
		}
		services = append(services, &service)
	}

	return services, nil
}

// WatchService watches for changes in a service and triggers a callback.
func (e *EtcdRegistry) WatchService(ctx context.Context, name string, callback func([]*ServiceInfo)) error {
	if e.client == nil {
		return fmt.Errorf("etcd client is not initialized")
	}
	if callback == nil {
		return fmt.Errorf("callback cannot be nil")
	}

	keyPrefix := fmt.Sprintf("%s/%s/services/%s/", e.opts.KeyPrefix, e.opts.Namespace, name)
	e.logger.Infof("Watching for service changes with key prefix: %s", keyPrefix)

	initialServices, err := e.GetService(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get initial service list: %w", err)
	}
	callback(initialServices)

	go func() {
		watchChan := e.client.Watch(ctx, keyPrefix, clientv3.WithPrefix())
		for {
			select {
			case <-ctx.Done():
				e.logger.Infof("Context for watching service [%s] is cancelled, stopping watch.", name)
				return
			case resp, ok := <-watchChan:
				if !ok {
					e.logger.Warnf("Watch channel for service [%s] is closed", name)
					return
				}
				if resp.Canceled {
					e.logger.Warnf("Watch for service [%s] was cancelled by etcd", name)
					return
				}
				e.logger.Infof("Service [%s] changed, re-fetching list", name)
				services, err := e.GetService(ctx, name)
				if err != nil {
					e.logger.Errorf("Failed to get service list during watch: %v", err)
					continue
				}
				callback(services)
			}
		}
	}()

	return nil
}

// WatchNamespace watches for all service changes in the current namespace and triggers a callback
// with the full service instance list in that namespace.
func (e *EtcdRegistry) WatchNamespace(ctx context.Context, callback func([]*ServiceInfo)) error {
	if e.client == nil {
		return fmt.Errorf("etcd client is not initialized")
	}
	if callback == nil {
		return fmt.Errorf("callback cannot be nil")
	}

	keyPrefix := fmt.Sprintf("%s/%s/services/", e.opts.KeyPrefix, e.opts.Namespace)
	e.logger.Infof("Watching for namespace service changes with key prefix: %s", keyPrefix)

	initialServices, err := e.GetNamespaceServices(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial namespace service list: %w", err)
	}
	callback(initialServices)

	go func() {
		watchChan := e.client.Watch(ctx, keyPrefix, clientv3.WithPrefix())
		for {
			select {
			case <-ctx.Done():
				e.logger.Infof("Context for namespace watch is cancelled, stopping watch.")
				return
			case resp, ok := <-watchChan:
				if !ok {
					e.logger.Warnf("Namespace watch channel is closed")
					return
				}
				if resp.Canceled {
					e.logger.Warnf("Namespace watch was cancelled by etcd")
					return
				}
				e.logger.Infof("Namespace [%s] services changed, re-fetching list", e.opts.Namespace)
				services, err := e.GetNamespaceServices(ctx)
				if err != nil {
					e.logger.Errorf("Failed to get namespace service list during watch: %v", err)
					continue
				}
				callback(services)
			}
		}
	}()

	return nil
}

// DeleteServiceKey manually deletes a service registration key.
func (e *EtcdRegistry) DeleteServiceKey(ctx context.Context, name, id string) error {
	if e.client == nil {
		return fmt.Errorf("etcd client is not initialized")
	}

	key := e.getServiceKey(name, id)
	e.logger.Infof("Deleting service registration key: %s", key)

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	resp, err := e.client.Delete(ctx, key)
	if err != nil {
		e.logger.Errorf("Failed to delete service key: %v, key: %s", err, key)
		return fmt.Errorf("failed to delete service key: %w", err)
	}

	if resp.Deleted == 0 {
		return fmt.Errorf("service key does not exist: %s", key)
	}

	e.logger.Infof("Successfully deleted service key: %s", key)
	return nil
}

// DeleteServiceByPrefix deletes multiple service registration keys by prefix.
func (e *EtcdRegistry) DeleteServiceByPrefix(ctx context.Context, namePrefix string) (int64, error) {
	if e.client == nil {
		return 0, fmt.Errorf("etcd client is not initialized")
	}

	keyPrefix := fmt.Sprintf("%s/%s/services/%s", e.opts.KeyPrefix, e.opts.Namespace, namePrefix)
	e.logger.Infof("Deleting services by prefix: %s", keyPrefix)

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	resp, err := e.client.Delete(ctx, keyPrefix, clientv3.WithPrefix())
	if err != nil {
		e.logger.Errorf("Failed to delete services by prefix: %v, keyPrefix: %s", err, keyPrefix)
		return 0, fmt.Errorf("failed to delete services by prefix: %w", err)
	}

	e.logger.Infof("Successfully deleted %d services (prefix: %s)", resp.Deleted, keyPrefix)
	return resp.Deleted, nil
}

// NewElection creates a new Election instance for a leadership campaign.
// The proposal is the value that this candidate is putting forward for the election.
// Typically this would be the candidate's own ID or address.
func (e *EtcdRegistry) NewElection(opts ElectionOptions) (*Election, error) {
	if opts.ElectionName == "" {
		return nil, fmt.Errorf("election name cannot be empty")
	}
	if opts.TTL == 0 {
		opts.TTL = 15 // Default to 15 seconds
	}
	if opts.Proposal == "" {
		// If the registry has a service registered, use its ID. Otherwise, use a random string.
		if e.serviceInfo != nil && e.serviceInfo.ID != "" {
			opts.Proposal = e.serviceInfo.ID
		} else {
			return nil, fmt.Errorf("proposal cannot be empty if no service is registered")
		}
	}

	// The session is the basis for leadership election.
	// If the session is lost, the leadership is lost.
	session, err := concurrency.NewSession(e.client, concurrency.WithTTL(opts.TTL))
	if err != nil {
		return nil, fmt.Errorf("failed to create concurrency session: %w", err)
	}

	electionPrefix := fmt.Sprintf("%s/%s/elections/%s", e.opts.KeyPrefix, e.opts.Namespace, opts.ElectionName)
	election := concurrency.NewElection(session, electionPrefix)

	return &Election{
		registry: e,
		session:  session,
		election: election,
		opts:     opts,
	}, nil
}
