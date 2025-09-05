package discovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIntegration_Election_SingleCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, _, cleanup := setupEtcd(ctx, t)
	defer cleanup()

	electionName := "single-candidate-test"
	proposal := "candidate-1"

	election, err := registry.NewElection(ElectionOptions{
		ElectionName: electionName,
		Proposal:     proposal,
		TTL:          5,
	})
	assert.NoError(t, err)

	// Campaign for leadership in a goroutine
	go func() {
		// The campaign will exit when the context is cancelled or leadership is lost.
		_ = election.Campaign(ctx)
	}()

	// Assert that the candidate becomes the leader
	assert.Eventually(t, func() bool {
		return election.IsLeader()
	}, 5*time.Second, 100*time.Millisecond, "candidate should become leader")

	// Check who is the leader
	leader, err := election.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, proposal, leader)

	// Resign from leadership
	err = election.Resign(context.Background()) // Use a new context for resign
	assert.NoError(t, err)

	// Check that there is no leader
	_, err = election.Leader(ctx)
	assert.Error(t, err, "expected error when there is no leader")
	assert.False(t, election.IsLeader())
}

func TestIntegration_Election_MultipleCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Setup: Two separate registries (simulating two nodes)
	reg1, reg2, cleanup := setupTwoRegistries(ctx, t)
	defer cleanup()

	electionName := "multi-candidate-test"
	proposal1 := "candidate-1"
	proposal2 := "candidate-2"

	elec1, err := reg1.NewElection(ElectionOptions{ElectionName: electionName, Proposal: proposal1, TTL: 5})
	assert.NoError(t, err)
	elec2, err := reg2.NewElection(ElectionOptions{ElectionName: electionName, Proposal: proposal2, TTL: 5})
	assert.NoError(t, err)

	wg := sync.WaitGroup{}
	wg.Add(1)

	// Candidate 1 campaigns and becomes leader
	go func() {
		defer wg.Done()
		_ = elec1.Campaign(ctx)
		t.Log("elec1 campaign ended")
	}()

	// Assert that candidate 1 becomes the leader
	assert.Eventually(t, func() bool { return elec1.IsLeader() }, 5*time.Second, 100*time.Millisecond, "elec1 should become leader")

	// Verify candidate 1 is the leader from another node's perspective
	leader, err := elec2.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, proposal1, leader)
	assert.False(t, elec2.IsLeader())

	wg.Add(1)
	// Candidate 2 campaigns. It should block until candidate 1 resigns.
	go func() {
		defer wg.Done()
		t.Log("elec2 campaigning")
		_ = elec2.Campaign(ctx)
		t.Log("elec2 campaign ended")
	}()

	// Wait for a moment, elec2 should still be waiting
	time.Sleep(1 * time.Second)
	assert.False(t, elec2.IsLeader(), "elec2 should not be leader yet")

	// Now, resign from elec1
	t.Log("elec1 resigning")
	err = elec1.Resign(context.Background())
	assert.NoError(t, err)
	t.Log("elec1 resigned")

	// After elec1 resigns, elec2 should become the leader
	assert.Eventually(t, func() bool { return elec2.IsLeader() }, 10*time.Second, 200*time.Millisecond, "elec2 should become leader")

	// Verify from another node's perspective
	observer, err := reg1.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "observer"})
	assert.NoError(t, err)
	currentLeader, err := observer.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, proposal2, currentLeader)

	// Final state check
	assert.False(t, elec1.IsLeader(), "elec1 should not be leader after resigning")
	assert.True(t, elec2.IsLeader(), "elec2 should be leader now")

	// Cleanup
	cancel() // Cancel the main context to stop campaigns
	wg.Wait()  // Wait for goroutines to finish
}

func TestIntegration_Election_Observe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg1, reg2, cleanup := setupTwoRegistries(ctx, t)
	defer cleanup()

	electionName := "observe-test"

	// Observer (doesn't campaign)
	elecObserver, err := reg2.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "observer"})
	assert.NoError(t, err)

	observeCtx, observeCancel := context.WithCancel(ctx)
	defer observeCancel()
	leaderCh := elecObserver.Observe(observeCtx)

	// Candidate
	elec1, err := reg1.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "leader-1", TTL: 5})
	assert.NoError(t, err)

	// 1. First leader campaigns
	go func() { _ = elec1.Campaign(ctx) }()

	// Check observer channel
	select {
	case leader := <-leaderCh:
		assert.Equal(t, "leader-1", leader)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first leader notification")
	}

	// 2. First leader resigns
	go func() {
		time.Sleep(1 * time.Second)
		_ = elec1.Resign(context.Background())
	}()

	// The observer channel should not receive anything immediately, as there is no leader.
	// The etcd Observe API only sends updates on new elections, not on deletions of the current leader.

	// 3. A new leader campaigns
	reg3, err := NewEtcdRegistry(reg1.client.Endpoints(), WithLogger(reg1.logger))
	assert.NoError(t, err)
	defer reg3.Close()
	elec2, err := reg3.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "leader-2", TTL: 5})
	assert.NoError(t, err)

	go func() { _ = elec2.Campaign(ctx) }()

	// Check observer channel for the new leader
	select {
	case leader := <-leaderCh:
		assert.Equal(t, "leader-2", leader)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for second leader notification")
	}

	_ = elec2.Resign(context.Background())
}

func TestIntegration_Election_SessionDone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// Manually setup etcd container to have fine-grained control over cleanup
	reg1, reg2, cleanup := setupTwoRegistries(ctx, t)
	defer cleanup() // This will close reg2 and terminate the container

	electionName := "session-done-test"

	elec1, err := reg1.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "leader-to-die", TTL: 5})
	assert.NoError(t, err)
	elec2, err := reg2.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "new-leader", TTL: 5})
	assert.NoError(t, err)

	// Leader 1 campaigns and wins
	campaignCtx, campaignCancel := context.WithCancel(ctx)
	defer campaignCancel()
	go func() { _ = elec1.Campaign(campaignCtx) }()

	assert.Eventually(t, func() bool { return elec1.IsLeader() }, 5*time.Second, 100*time.Millisecond, "elec1 should become leader")

	leader, err := elec2.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "leader-to-die", leader)

	// Watch for leadership loss
	leadershipLost := make(chan struct{})
	go func() {
		<-elec1.Done() // This blocks until the session is lost
		close(leadershipLost)
	}()

	// Candidate 2 campaigns, should block
	go func() { _ = elec2.Campaign(ctx) }()

	// Manually close the first registry's client to kill the session
	t.Log("Closing registry 1 to kill leader session")
	err = reg1.Close() // This closes the client, killing the session
	assert.NoError(t, err)

	// Check that leadership was lost
	select {
	case <-leadershipLost:
		t.Log("Successfully detected leadership loss via Done() channel")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for leadership loss notification")
	}

	// Now, the second candidate should become the leader
	assert.Eventually(t, func() bool {
		leader, err := elec2.Leader(context.Background())
		return err == nil && leader == "new-leader"
	}, 10*time.Second, 200*time.Millisecond, "elec2 should become leader")

	assert.True(t, elec2.IsLeader())
}

// setupTwoRegistries is a helper for tests needing two separate registry instances
// sharing the same etcd container.
func setupTwoRegistries(ctx context.Context, t *testing.T) (*EtcdRegistry, *EtcdRegistry, func()) {
	reg1, _, cleanup1 := setupEtcd(ctx, t)

	reg2, err := NewEtcdRegistry(reg1.client.Endpoints(), WithLogger(reg1.logger))
	assert.NoError(t, err)

	cleanup := func() {
		_ = reg2.Close()
		cleanup1() // This closes reg1 and terminates the container
	}

	return reg1, reg2, cleanup
}