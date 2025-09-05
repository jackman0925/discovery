package discovery

import (
	"context"
	"fmt"
	"sync"

	"go.etcd.io/etcd/client/v3/concurrency"
)

// Election manages a leadership election campaign.
type Election struct {
	registry *EtcdRegistry
	session  *concurrency.Session
	election *concurrency.Election
	opts     ElectionOptions

	campaignOnce sync.Once
	resignOnce   sync.Once
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// ElectionOptions holds configuration for an Election.
type ElectionOptions struct {
	// ElectionName is the name of the election, which will be used as a prefix for etcd keys.
	ElectionName string
	// Proposal is the value this candidate proposes for the election.
	// If it's empty, a default value will be generated.
	Proposal string
	// TTL is the session TTL in seconds for the leadership election.
	TTL int
}

// Campaign puts a candidate into a leadership election.
// This method blocks until the candidate wins the election or the context is cancelled.
// After winning, it will hold the leadership until the context is cancelled,
// the underlying session is closed, or Resign is called.
func (e *Election) Campaign(ctx context.Context) error {
	e.registry.logger.Printf("[INFO] Campaigning for leadership in election '%s' with proposal '%s'", e.opts.ElectionName, e.opts.Proposal)
	err := e.election.Campaign(ctx, e.opts.Proposal)
	if err != nil {
		e.registry.logger.Printf("[ERROR] Failed to campaign for leadership in election '%s': %v", e.opts.ElectionName, err)
		return fmt.Errorf("failed to campaign for leadership: %w", err)
	}
	e.registry.logger.Printf("[INFO] Won leadership in election '%s'", e.opts.ElectionName)
	return nil
}

// Leader returns the current leader's proposal.
// This is a non-blocking call and may return an error if the leader is not yet known.
func (e *Election) Leader(ctx context.Context) (string, error) {
	resp, err := e.election.Leader(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current leader: %w", err)
	}
	if resp != nil && len(resp.Kvs) > 0 {
		return string(resp.Kvs[0].Value), nil
	}
	return "", concurrency.ErrElectionNoLeader
}

// Observe returns a channel that streams leader changes.
// When a new leader is elected, it will be sent on the channel.
// If the context is cancelled, the channel will be closed.
func (e *Election) Observe(ctx context.Context) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		obsCh := e.election.Observe(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-obsCh:
				if !ok {
					return
				}
				if len(resp.Kvs) > 0 {
					select {
					case ch <- string(resp.Kvs[0].Value):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch
}

// Resign gives up leadership. If the candidate is not a leader, this is a no-op.
// It returns an error if the resignation fails.
func (e *Election) Resign(ctx context.Context) error {
	e.registry.logger.Printf("[INFO] Resigning from leadership in election '%s'", e.opts.ElectionName)
	err := e.election.Resign(ctx)
	if err != nil {
		e.registry.logger.Printf("[ERROR] Failed to resign from leadership: %v", err)
		return fmt.Errorf("failed to resign: %w", err)
	}
	// Closing the session ensures all resources are released.
	return e.session.Close()
}

// Done returns a channel that is closed when the underlying session is lost.
// This indicates that leadership is lost and the candidate should stop any leader-specific work.
func (e *Election) Done() <-chan struct{} {
	return e.session.Done()
}

// IsLeader returns true if this candidate is currently the leader.
// Note: This method performs a network call to etcd to get the current leader.
func (e *Election) IsLeader() bool {
	leader, err := e.Leader(context.Background())
	if err != nil {
		return false
	}
	return leader == e.opts.Proposal
}