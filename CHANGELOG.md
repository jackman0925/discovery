# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.1] - 2026-02-21

### Added
- Added `GetNamespaceServices` and `WatchNamespace` to support full service watching within a namespace.
- Added integration test `TestIntegration_WatchNamespace` for namespace-level watch behavior.
- Added unit tests for watch/get edge cases on uninitialized client and nil callback.

### Fixed
- Added defensive checks in `GetService`, `GetNamespaceServices`, `WatchService`, and `WatchNamespace` to return clear errors when etcd client is not initialized.
- Added callback validation for `WatchService` and `WatchNamespace` to prevent nil callback panic.
- Improved watch loop stability by handling closed watch channels (`ok == false`) in both `WatchService` and `WatchNamespace`.

## [v0.2.0] - 2025-09-08

### Changed
- **[BREAKING CHANGE]** Refactored the logging mechanism to be more flexible and modern.
  - The `Logger` interface no longer includes the `Printf` method. It now exclusively uses leveled methods: `Errorf`, `Warnf`, `Infof`, and `Debugf`.
  - The `WithLoggerAndLevel` option remains available to easily adapt standard Go loggers (which have a `Printf` method).
- Updated `README.md` with a new `Prerequisites` section, including a Docker command to start an etcd instance.
- Re-ordered sections in `README.md` for better readability.

### Fixed
- Corrected and improved the code examples in `README.md` to make them runnable and to better demonstrate library usage.

## [v0.1.0] - 2025-09-05

### Added
- **Leader Election**: Added a new leader election feature, allowing a single node among a group of services to be elected as a leader.
- A new `Election` struct with methods like `Campaign`, `Resign`, `Leader`, `Observe`, and `IsLeader`.
- A `NewElection` factory method on the `EtcdRegistry` to create election instances.
- Comprehensive integration tests for the leader election feature in `election_test.go`.

### Changed
- Updated `README.md` with a new "Leader Election" section, including documentation and a complete usage example.
- Improved the reliability of integration tests by replacing fixed `time.Sleep` calls with `assert.Eventually` to prevent race conditions.
- Refactored test cleanup logic to use `context.Background()` when terminating test containers, fixing "context canceled" errors.

## [v0.0.1]

### Added
- Comprehensive integration test suite using `testcontainers-go`.
- Unit tests for edge cases and error conditions.
- `TestIntegration_WatchWithContextCancel` to ensure proper shutdown of watch goroutines.
- A `CHANGELOG.md` file to document changes.
- Badges for "Go Report Card", "Go Reference", and "License" in `README.md`.
- A "Testing" section in `README.md`.

### Changed
- Updated `go.mod` to set the correct module path for `go get`.
- Made `Deregister` function idempotent to prevent panics on multiple calls.
- Refactored `setupEtcd` test helper to use a specific `bitnami/etcd` image and a more robust wait strategy.
- Corrected the test logic for `TestIntegration_WatchService` to use a single etcd instance.
- Updated `README.md` with correct installation paths and a new "Testing" section.

### Fixed
- A bug in `NewEtcdRegistry` where it would not return an error for invalid endpoints.
- A race condition in `Deregister` that could cause a panic when called multiple times.

### Removed
- The flawed `TestIntegration_AutoReRegister` test, which was not correctly testing the re-registration logic.
