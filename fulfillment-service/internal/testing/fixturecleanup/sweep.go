package fixturecleanup

import (
	"context"
	"fmt"
	"time"
)

// Stats records actual callback work and scheduler idle time, including partial
// progress on error or panic. Drain updates the caller-owned value synchronously.
type Stats struct {
	TenantsStarted        int
	TenantsCompleted      int
	TenantsWithoutPending int
	DeleteAttempts        int
	PendingResponses      int
	DeleteCallsNS         int64
	WaitNS                int64
}

// Drain progresses at most two tenant identities with one synchronous deletion
// call at a time. Each identity starts its fixed two-minute budget and one-second
// ticker on first processing. The supplied stats must be nonnil and unshared.
// Cancellation is cooperative: a callback that ignores its context must return
// before Drain can finish or dispatch further work.
func Drain(ctx context.Context, tenantNames []string, deleteTenant func(context.Context, string) error, isPending func(error) bool, stats *Stats) error {
	return drain(ctx, tenantNames, deleteTenant, isPending, stats, 2*time.Minute, time.Second)
}

func drain(ctx context.Context, tenantNames []string, deleteTenant func(context.Context, string) error, isPending func(error) bool, stats *Stats, budget, interval time.Duration) error {
	*stats = Stats{}
	active := make([]*tenantWork, 0, 2)
	defer func() {
		for _, work := range active {
			work.close()
		}
	}()
	next, previous := 0, -1
	for {
		for len(active) < 2 && next < len(tenantNames) {
			active = append(active, &tenantWork{name: tenantNames[next], ordinal: next, ready: true})
			next++
		}
		if len(active) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			for _, work := range active {
				if work.pending {
					ownErr := work.ctx.Err()
					if ownErr == nil {
						ownErr = err
					}
					return fmt.Errorf("wait for default networking deletion for tenant %q: %w", work.name, ownErr)
				}
			}
			return fmt.Errorf("delete default networking for tenant %q: %w", active[0].name, err)
		}
		if expired := expiredPeer(active, nil); expired != nil {
			return fmt.Errorf("wait for default networking deletion for tenant %q: %w", expired.name, expired.err())
		}
		for _, work := range active {
			if work.pending {
				select {
				case <-work.ticker.C:
					work.ready = true
				default:
				}
			}
		}
		var selected *tenantWork
		for offset := 1; offset <= len(active); offset++ {
			index := (previous + offset) % len(active)
			if active[index].ready {
				selected = active[index]
				previous = index
				break
			}
		}
		if selected == nil {
			wait(ctx, active, stats)
			continue
		}
		if selected.ctx == nil {
			for index, work := range active {
				if work.ctx == nil && work.ordinal < selected.ordinal {
					selected = work
					previous = index
					break
				}
			}
		}
		selected.ready = false
		if selected.ctx == nil {
			selected.ctx, selected.cancel = context.WithTimeout(ctx, budget)
			selected.deadline, _ = selected.ctx.Deadline()
			selected.ticker = time.NewTicker(interval)
			stats.TenantsStarted++
		}
		deadline := selected.deadline
		// A blocked cooperative callback must yield at an earlier pending peer's
		// fixed deadline; synchronous calls cannot forcibly preempt other work.
		for _, peer := range active {
			if peer != selected && peer.pending && peer.deadline.Before(deadline) {
				deadline = peer.deadline
			}
		}
		stats.DeleteAttempts++
		err := call(selected, deadline, deleteTenant, stats)
		pending := err != nil && isPending(err)
		if err == nil {
			stats.TenantsCompleted++
			if !selected.pending {
				stats.TenantsWithoutPending++
			}
		} else if pending {
			stats.PendingResponses++
			selected.pending = true
		}
		if parentErr := ctx.Err(); parentErr != nil {
			if err == nil {
				err = parentErr
			}
			return fmt.Errorf("delete default networking for tenant %q: %w", selected.name, err)
		}
		if expired := expiredPeer(active, selected); expired != nil {
			return fmt.Errorf("wait for default networking deletion for tenant %q: %w", expired.name, expired.err())
		}
		if err != nil && !pending {
			return fmt.Errorf("delete default networking for tenant %q: %w", selected.name, err)
		}
		if err == nil {
			if ownErr := selected.err(); ownErr != nil {
				return fmt.Errorf("delete default networking for tenant %q: %w", selected.name, ownErr)
			}
			selected.close()
			for index, work := range active {
				if work == selected {
					active = append(active[:index], active[index+1:]...)
					break
				}
			}
		}
	}
}

type tenantWork struct {
	name     string
	ordinal  int
	ctx      context.Context
	cancel   context.CancelFunc
	ticker   *time.Ticker
	deadline time.Time
	pending  bool
	ready    bool
}

func (work *tenantWork) close() {
	if work.ticker != nil {
		work.ticker.Stop()
	}
	if work.cancel != nil {
		work.cancel()
	}
}

func (work *tenantWork) err() error {
	if err := work.ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(work.deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func expiredPeer(active []*tenantWork, current *tenantWork) *tenantWork {
	var expired *tenantWork
	for _, peer := range active {
		if !peer.pending || peer == current || (current != nil && !peer.deadline.Before(current.deadline)) || peer.err() == nil {
			continue
		}
		if expired == nil || peer.deadline.Before(expired.deadline) || (peer.deadline.Equal(expired.deadline) && peer.ordinal < expired.ordinal) {
			expired = peer
		}
	}
	return expired
}

func call(work *tenantWork, deadline time.Time, deleteTenant func(context.Context, string) error, stats *Stats) error {
	callCtx, cancel := context.WithDeadline(work.ctx, deadline)
	defer cancel()
	started := time.Now()
	defer func() { stats.DeleteCallsNS += time.Since(started).Nanoseconds() }()
	return deleteTenant(callCtx, work.name)
}

func wait(ctx context.Context, active []*tenantWork, stats *Stats) {
	started := time.Now()
	defer func() { stats.WaitNS += time.Since(started).Nanoseconds() }()
	first := active[0]
	var secondDone <-chan struct{}
	var secondTick <-chan time.Time
	if len(active) == 2 {
		secondDone = active[1].ctx.Done()
		secondTick = active[1].ticker.C
	}
	select {
	case <-ctx.Done():
	case <-first.ctx.Done():
	case <-secondDone:
	case <-first.ticker.C:
		first.ready = true
	case <-secondTick:
		active[1].ready = true
	}
}
