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

	// Campaign for leadership
	go func() {
		err := election.Campaign(ctx)
		assert.NoError(t, err)
	}()

	// Give it a moment to win the election
	time.Sleep(1 * time.Second)

	// Check who is the leader
	leader, err := election.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, proposal, leader)
	assert.True(t, election.IsLeader())

	// Resign from leadership
	err = election.Resign(ctx)
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
	reg1, _, cleanup1 := setupEtcd(ctx, t)
	defer cleanup1()
	reg2, err := NewEtcdRegistry(reg1.client.Endpoints(), WithLogger(reg1.logger))
	assert.NoError(t, err)
	defer reg2.Close()

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
		// This will block until it becomes leader
		err := elec1.Campaign(ctx)
		if err != nil {
			t.Logf("elec1 campaign error: %v", err)
		}
		t.Log("elec1 became leader")

		// Hold leadership for a few seconds
		time.Sleep(4 * time.Second)

		// Resign
		t.Log("elec1 resigning")
		err = elec1.Resign(context.Background()) // Use background context for resign
		assert.NoError(t, err)
		t.Log("elec1 resigned")
	}()

	// Give candidate 1 a head start
	time.Sleep(1 * time.Second)

	// Verify candidate 1 is the leader
	leader, err := elec2.Leader(ctx) // elec2 can check the leader
	assert.NoError(t, err)
	assert.Equal(t, proposal1, leader)
	assert.True(t, elec1.IsLeader())
	assert.False(t, elec2.IsLeader())

	wg.Add(1)
	// Candidate 2 campaigns. It should block until candidate 1 resigns.
	go func() {
		defer wg.Done()
		t.Log("elec2 campaigning")
		err := elec2.Campaign(ctx)
		if err != nil {
			t.Logf("elec2 campaign error: %v", err)
		}
		t.Log("elec2 became leader")
	}()

	// Wait for a moment, elec2 should still be waiting
	time.Sleep(1 * time.Second)
	assert.False(t, elec2.IsLeader())

	// After elec1 resigns, elec2 should become the leader
	time.Sleep(5 * time.Second) // Wait for resignation and new campaign to complete

	leader, err = elec1.Leader(ctx) // elec1 can check the leader
	assert.NoError(t, err)
	assert.Equal(t, proposal2, leader)
	assert.False(t, elec1.IsLeader())
	assert.True(t, elec2.IsLeader())

	// Cleanup
	_ = elec2.Resign(context.Background())
	wg.Wait()
}

func TestIntegration_Election_Observe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg1, _, cleanup1 := setupEtcd(ctx, t)
	defer cleanup1()
	reg2, err := NewEtcdRegistry(reg1.client.Endpoints(), WithLogger(reg1.logger))
	assert.NoError(t, err)
	defer reg2.Close()

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg1, _, cleanup1 := setupEtcd(ctx, t)
	// NO defer cleanup1() here, we will close it manually

	reg2, err := NewEtcdRegistry(reg1.client.Endpoints(), WithLogger(reg1.logger))
	assert.NoError(t, err)
	defer reg2.Close()

	electionName := "session-done-test"

	elec1, err := reg1.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "leader-to-die", TTL: 5})
	assert.NoError(t, err)
	elec2, err := reg2.NewElection(ElectionOptions{ElectionName: electionName, Proposal: "new-leader", TTL: 5})
	assert.NoError(t, err)

	// Leader 1 campaigns and wins
	campaignCtx, campaignCancel := context.WithCancel(ctx)
	go func() { _ = elec1.Campaign(campaignCtx) }()
	time.Sleep(1 * time.Second) // let it win

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
	cleanup1() // This closes the client

	// Check that leadership was lost
	select {
	case <-leadershipLost:
		// Success
		t.Log("Successfully detected leadership loss via Done() channel")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for leadership loss notification")
	}

	// Stop the first campaign goroutine
	campaignCancel()

	// Now, the second candidate should become the leader
	time.Sleep(2 * time.Second) // give it time to campaign

	leader, err = elec2.Leader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "new-leader", leader)

	_ = elec2.Resign(context.Background())
}
