package fixturecleanup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var errPending = errors.New("resources deleting")

var _ = Describe("Drain", func() {
	It("progresses a second tenant before retrying the first pending tenant", func() {
		var stats Stats
		var visited []string
		secondVisited := false
		err := drain(context.Background(), []string{"first", "second"}, func(_ context.Context, name string) error {
			visited = append(visited, name)
			if name == "second" {
				secondVisited = true
				return nil
			}
			if len(visited) == 1 {
				return errPending
			}
			if !secondVisited {
				return errors.New("first retried before second progressed")
			}
			return nil
		}, func(err error) bool { return errors.Is(err, errPending) }, &stats, time.Second, time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		Expect(visited).To(Equal([]string{"first", "second", "first"}))
		Expect(stats.TenantsCompleted).To(Equal(2))
	})

	DescribeTable("empty input", func(canceled bool) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if canceled {
			cancel()
		}
		stats := Stats{DeleteAttempts: 7}
		err := Drain(ctx, nil, func(context.Context, string) error {
			Fail("empty input called Delete")
			return nil
		}, isDeleting, &stats)
		Expect(err).ToNot(HaveOccurred())
		Expect(stats).To(Equal(Stats{}))
	}, Entry("live parent", false), Entry("canceled parent", true))

	It("keeps the complete original order including repeated identities", func() {
		var stats Stats
		var names []string
		var contexts []context.Context
		err := Drain(context.Background(), []string{"first", "first", "third"}, func(ctx context.Context, name string) error {
			names = append(names, name)
			contexts = append(contexts, ctx)
			return nil
		}, isDeleting, &stats)
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(Equal([]string{"first", "first", "third"}))
		Expect(stats.TenantsStarted).To(Equal(3))
		Expect(stats.TenantsCompleted).To(Equal(3))
		Expect(stats.TenantsWithoutPending).To(Equal(3))
		Expect(stats.DeleteAttempts).To(Equal(3))
		Expect(stats.PendingResponses).To(BeZero())
		Expect(stats.WaitNS).To(BeZero())
		for _, ctx := range contexts {
			Expect(ctx.Err()).To(Equal(context.Canceled))
		}
	})

	It("retries wrapped pending responses with two active identities and one call", func() {
		var stats Stats
		attempts := map[string]int{}
		active := map[string]bool{}
		inFlight, peak := 0, 0
		started := time.Now()
		err := drain(context.Background(), []string{"first", "second", "third", "fourth"}, func(_ context.Context, name string) error {
			inFlight++
			defer func() { inFlight-- }()
			Expect(inFlight).To(Equal(1))
			active[name] = true
			if len(active) > peak {
				peak = len(active)
			}
			Expect(len(active)).To(BeNumerically("<=", 2))
			attempts[name]++
			if attempts[name] < 3 {
				return fmt.Errorf("wrapped: %w", errPending)
			}
			delete(active, name)
			return nil
		}, isDeleting, &stats, time.Second, 5*time.Millisecond)
		elapsed := time.Since(started).Nanoseconds()
		Expect(err).ToNot(HaveOccurred())
		Expect(peak).To(Equal(2))
		Expect(stats.TenantsCompleted).To(Equal(4))
		Expect(stats.TenantsWithoutPending).To(BeZero())
		Expect(stats.DeleteAttempts).To(Equal(12))
		Expect(stats.PendingResponses).To(Equal(8))
		Expect(stats.WaitNS).To(BeNumerically(">", 0))
		Expect(stats.WaitNS + stats.DeleteCallsNS).To(BeNumerically("<=", elapsed))
	})

	It("does not starve a ready pending peer behind immediate-completion refills", func() {
		var stats Stats
		var visited []string
		firstAttempts := 0
		err := drain(context.Background(), []string{"first", "second", "third", "fourth", "fifth"}, func(_ context.Context, name string) error {
			visited = append(visited, name)
			if name != "first" {
				<-time.After(15 * time.Millisecond)
				return nil
			}
			firstAttempts++
			if firstAttempts < 3 {
				return errPending
			}
			return nil
		}, isDeleting, &stats, time.Second, 5*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		Expect(visited[:5]).To(Equal([]string{"first", "second", "first", "third", "first"}))
	})

	It("preserves nonpending error identity and cause without processing later work", func() {
		var stats Stats
		cause := errors.New("denied")
		var visited []string
		err := Drain(context.Background(), []string{"first", "second", "third"}, func(_ context.Context, name string) error {
			visited = append(visited, name)
			return cause
		}, isDeleting, &stats)
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(err.Error()).To(Equal(`delete default networking for tenant "first": denied`))
		Expect(visited).To(Equal([]string{"first"}))
		Expect(stats.TenantsStarted).To(Equal(1))
		Expect(stats.DeleteAttempts).To(Equal(1))
		Expect(stats.TenantsCompleted).To(BeZero())
	})

	It("retains partial progress on an ordinary error and closes every started call", func() {
		var stats Stats
		var contexts []context.Context
		cause := errors.New("third tenant failed")
		err := Drain(context.Background(), []string{"first", "second", "third", "fourth"}, func(ctx context.Context, name string) error {
			contexts = append(contexts, ctx)
			switch name {
			case "first":
				return nil
			case "second":
				return errPending
			default:
				return cause
			}
		}, isDeleting, &stats)
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(err.Error()).To(HavePrefix(`delete default networking for tenant "third"`))
		Expect(stats.TenantsStarted).To(Equal(3))
		Expect(stats.TenantsCompleted).To(Equal(1))
		Expect(stats.TenantsWithoutPending).To(Equal(1))
		Expect(stats.DeleteAttempts).To(Equal(3))
		Expect(stats.PendingResponses).To(Equal(1))
		for _, ctx := range contexts {
			Expect(ctx.Err()).To(Equal(context.Canceled))
		}
	})

	It("does no work for a nonempty canceled input and names the first identity", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stats Stats
		err := Drain(ctx, []string{"first", "second"}, func(context.Context, string) error {
			Fail("precanceled input called Delete")
			return nil
		}, isDeleting, &stats)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`delete default networking for tenant "first"`))
		Expect(stats).To(Equal(Stats{}))
	})

	DescribeTable("parent cancellation during a callback", func(callbackError bool) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var stats Stats
		cause := errors.New("callback cause")
		err := Drain(ctx, []string{"first", "second"}, func(context.Context, string) error {
			cancel()
			if callbackError {
				return cause
			}
			return nil
		}, isDeleting, &stats)
		Expect(err.Error()).To(ContainSubstring(`delete default networking for tenant "first"`))
		if callbackError {
			Expect(errors.Is(err, cause)).To(BeTrue())
			Expect(stats.TenantsCompleted).To(BeZero())
		} else {
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			Expect(stats.TenantsCompleted).To(Equal(1))
			Expect(stats.TenantsWithoutPending).To(Equal(1))
		}
		Expect(stats.DeleteAttempts).To(Equal(1))
	}, Entry("retains actual callback error", true), Entry("counts nil but cannot report success", false))

	It("propagates cancellation to a cooperatively blocking callback", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var stats Stats
		err := Drain(ctx, []string{"first", "second"}, func(callCtx context.Context, _ string) error {
			<-callCtx.Done()
			return callCtx.Err()
		}, isDeleting, &stats)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`delete default networking for tenant "first"`))
		Expect(stats.DeleteAttempts).To(Equal(1))
	})

	It("returns the oldest pending tenant's WAIT error on idle parent cancellation", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var stats Stats
		err := Drain(ctx, []string{"first", "second", "third"}, func(context.Context, string) error {
			return errPending
		}, isDeleting, &stats)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`wait for default networking deletion for tenant "first"`))
		Expect(stats.DeleteAttempts).To(Equal(2))
		Expect(stats.WaitNS).To(BeNumerically(">", 0))
	})

	It("propagates explicit parent cancellation during an idle wait and joins the canceler", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan context.Context, 1)
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			callCtx := <-returned
			<-callCtx.Done()
			<-time.After(15 * time.Millisecond)
			cancel()
		}()
		var stats Stats
		err := Drain(ctx, []string{"first"}, func(callCtx context.Context, _ string) error {
			returned <- callCtx
			return errPending
		}, isDeleting, &stats)
		<-joined
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(err.Error()).To(HavePrefix(`wait for default networking deletion for tenant "first"`))
		Expect(stats.WaitNS).To(BeNumerically(">", 0))
		Expect(stats.DeleteAttempts).To(Equal(1))
	})

	It("retains the parent cancellation cause before gated child propagation during idle wait", func() {
		base, cancel := context.WithCancel(context.Background())
		parent := &gatedCancellationContext{
			Context: context.Background(), base: base,
			idle: make(chan struct{}), release: make(chan struct{}),
		}
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			<-parent.idle
			cancel()
		}()
		DeferCleanup(func() {
			cancel()
			close(parent.release)
			parent.workers.Wait()
			<-joined
		})
		var stats Stats
		var visits []string
		err := Drain(parent, []string{"first", "second", "third"}, func(_ context.Context, name string) error {
			visits = append(visits, name)
			if name == "second" {
				parent.armed.Store(true)
			}
			return errPending
		}, isDeleting, &stats)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "parent cause must survive delayed child propagation: %v", err)
		Expect(err.Error()).To(HavePrefix(`wait for default networking deletion for tenant "first"`))
		Expect(visits).To(Equal([]string{"first", "second"}))
		Expect(stats.DeleteAttempts).To(Equal(2))
		Expect(stats.WaitNS).To(BeNumerically(">", 0))
	})

	DescribeTable("own deadline", func(block bool) {
		var stats Stats
		err := drain(context.Background(), []string{"first"}, func(ctx context.Context, _ string) error {
			if block {
				<-ctx.Done()
				return ctx.Err()
			}
			return errPending
		}, isDeleting, &stats, 40*time.Millisecond, time.Second)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		branch := "wait"
		if block {
			branch = "delete"
		}
		Expect(err.Error()).To(HavePrefix(branch + " "))
		Expect(stats.DeleteAttempts).To(Equal(1))
	}, Entry("in a blocking call", true), Entry("while idle", false))

	It("does not mask its own expiration with a nil callback result", func() {
		var stats Stats
		err := drain(context.Background(), []string{"first", "second"}, func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return nil
		}, isDeleting, &stats, 40*time.Millisecond, time.Second)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(HavePrefix(`delete default networking for tenant "first"`))
		Expect(stats.TenantsCompleted).To(Equal(1))
		Expect(stats.DeleteAttempts).To(Equal(1))
	})

	It("gives parent cancellation precedence over an expired peer and retains the callback cause", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		cause := errors.New("actual callback error")
		var stats Stats
		err := drain(ctx, []string{"first", "second", "third"}, func(callCtx context.Context, name string) error {
			if name == "first" {
				return errPending
			}
			<-callCtx.Done()
			return cause
		}, isDeleting, &stats, time.Second, time.Second)
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(err.Error()).To(HavePrefix(`delete default networking for tenant "second"`))
		Expect(stats.DeleteAttempts).To(Equal(2))
	})

	It("breaks equal expired deadlines by original ordinal", func() {
		deadline := time.Now().Add(-time.Second)
		firstCtx, firstCancel := context.WithDeadline(context.Background(), deadline)
		defer firstCancel()
		secondCtx, secondCancel := context.WithDeadline(context.Background(), deadline)
		defer secondCancel()
		first := &tenantWork{ctx: firstCtx, deadline: deadline, ordinal: 1, pending: true}
		second := &tenantWork{ctx: secondCtx, deadline: deadline, ordinal: 2, pending: true}
		Expect(expiredPeer([]*tenantWork{second, first}, nil)).To(BeIdenticalTo(first))
	})

	It("cancels own contexts and stops tickers when work is closed", func() {
		ctx, cancel := context.WithCancel(context.Background())
		work := &tenantWork{ctx: ctx, cancel: cancel, ticker: time.NewTicker(time.Millisecond)}
		work.close()
		Expect(ctx.Err()).To(Equal(context.Canceled))
		Consistently(work.ticker.C, 15*time.Millisecond).ShouldNot(Receive())
	})

	DescribeTable("expired peer wins newly possible callback result races", func(resultKind string) {
		var stats Stats
		var firstDeadline time.Time
		var inheritedDeadline time.Time
		var visited []string
		err := drain(context.Background(), []string{"first", "second", "third"}, func(ctx context.Context, name string) error {
			visited = append(visited, name)
			deadline, _ := ctx.Deadline()
			if name == "first" {
				firstDeadline = deadline
				return errPending
			}
			inheritedDeadline = deadline
			<-ctx.Done()
			switch resultKind {
			case "nil":
				return nil
			case "pending":
				return errPending
			default:
				return errors.New("simultaneous callback failure")
			}
		}, isDeleting, &stats, 45*time.Millisecond, time.Second)
		Expect(inheritedDeadline).To(Equal(firstDeadline))
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`wait for default networking deletion for tenant "first"`))
		Expect(visited).To(Equal([]string{"first", "second"}))
		if resultKind == "nil" {
			Expect(stats.TenantsCompleted).To(Equal(1))
		}
	}, Entry("nil result", "nil"), Entry("pending result", "pending"), Entry("permanent error", "error"))

	It("uses the real two-minute public budget to cancel a blocking peer at the exact earlier deadline", func() {
		var stats Stats
		var firstDeadline, peerDeadline time.Time
		var visited []string
		started := time.Now()
		err := Drain(context.Background(), []string{"first", "second", "third"}, func(ctx context.Context, name string) error {
			visited = append(visited, name)
			deadline, _ := ctx.Deadline()
			if name == "first" {
				firstDeadline = deadline
				return errPending
			}
			peerDeadline = deadline
			<-ctx.Done()
			return ctx.Err()
		}, isDeleting, &stats)
		returned := time.Now()
		Expect(firstDeadline.Sub(started)).To(BeNumerically(">=", 2*time.Minute))
		Expect(firstDeadline.Sub(started)).To(BeNumerically("<=", 2*time.Minute+time.Second))
		Expect(peerDeadline).To(Equal(firstDeadline))
		Expect(returned.Before(firstDeadline)).To(BeFalse())
		Expect(returned.Sub(firstDeadline)).To(BeNumerically("<=", time.Second))
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`wait for default networking deletion for tenant "first"`))
		Expect(visited).To(Equal([]string{"first", "second"}))
		Expect(stats.DeleteAttempts).To(Equal(2))
	})

	It("starts queued budgets and tickers only on first processing and never resets them", func() {
		var stats Stats
		var firstSecondCall, secondStarted time.Time
		var deadlines []time.Time
		attempt := 0
		budget, interval := 300*time.Millisecond, 45*time.Millisecond
		err := drain(context.Background(), []string{"first", "second"}, func(ctx context.Context, name string) error {
			if name == "first" {
				<-time.After(80 * time.Millisecond)
				return nil
			}
			attempt++
			deadline, _ := ctx.Deadline()
			deadlines = append(deadlines, deadline)
			if attempt == 1 {
				secondStarted = time.Now()
				Expect(time.Until(deadline)).To(BeNumerically(">", budget-30*time.Millisecond))
			}
			if attempt == 2 {
				firstSecondCall = time.Now()
			}
			if attempt < 3 {
				return errPending
			}
			return nil
		}, isDeleting, &stats, budget, interval)
		Expect(err).ToNot(HaveOccurred())
		Expect(firstSecondCall.Sub(secondStarted)).To(BeNumerically(">=", interval-5*time.Millisecond))
		Expect(deadlines).To(HaveLen(3))
		Expect(deadlines[1]).To(Equal(deadlines[0]))
		Expect(deadlines[2]).To(Equal(deadlines[0]))
	})

	It("does not return early from a noncooperative callback and joins the test worker", func() {
		var stats Stats
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		result := make(chan error, 1)
		finished := make(chan struct{})
		var releaseOnce sync.Once
		joined := false
		DeferCleanup(func() {
			releaseOnce.Do(func() { close(release) })
			if !joined {
				<-result
			}
			<-finished
		})
		go func() {
			defer close(finished)
			result <- drain(context.Background(), []string{"first", "second", "third"}, func(ctx context.Context, name string) error {
				if name == "first" {
					return errPending
				}
				entered <- ctx
				<-release
				return nil
			}, isDeleting, &stats, 50*time.Millisecond, time.Second)
		}()
		var callCtx context.Context
		Eventually(entered, time.Second).Should(Receive(&callCtx))
		Eventually(callCtx.Done(), time.Second).Should(BeClosed())
		select {
		case <-result:
			joined = true
			Fail("synchronous noncooperative callback returned before release")
		default:
		}
		releaseOnce.Do(func() { close(release) })
		var err error
		Eventually(result, time.Second).Should(Receive(&err))
		joined = true
		<-finished
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`wait for default networking deletion for tenant "first"`))
		Expect(stats.TenantsCompleted).To(Equal(1))
	})

	It("retains partial caller-owned stats and active elapsed time on the original panic", func() {
		var stats, snapshot Stats
		var contexts []context.Context
		var visited []string
		sentinel := errors.New("panic sentinel")
		run := func() {
			defer func() { snapshot = stats }()
			_ = drain(context.Background(), []string{"first", "second", "third", "fourth"}, func(ctx context.Context, name string) error {
				contexts = append(contexts, ctx)
				visited = append(visited, name)
				switch name {
				case "first":
					return nil
				case "second":
					return errPending
				default:
					<-time.After(5 * time.Millisecond)
					panic(sentinel)
				}
			}, isDeleting, &stats, time.Second, time.Second)
		}
		Expect(run).To(PanicWith(sentinel))
		Expect(snapshot).To(Equal(stats))
		Expect(snapshot.TenantsStarted).To(Equal(3))
		Expect(snapshot.TenantsCompleted).To(Equal(1))
		Expect(snapshot.TenantsWithoutPending).To(Equal(1))
		Expect(snapshot.DeleteAttempts).To(Equal(3))
		Expect(snapshot.PendingResponses).To(Equal(1))
		Expect(snapshot.DeleteCallsNS).To(BeNumerically(">=", int64(5*time.Millisecond)))
		Expect(visited).To(Equal([]string{"first", "second", "third"}))
		for _, ctx := range contexts {
			Expect(ctx.Err()).To(Equal(context.Canceled))
		}
	})
})

func isDeleting(err error) bool {
	return errors.Is(err, errPending)
}

type gatedCancellationContext struct {
	context.Context
	base    context.Context
	idle    chan struct{}
	release chan struct{}
	armed   atomic.Bool
	once    sync.Once
	workers sync.WaitGroup
}

func (parent *gatedCancellationContext) Done() <-chan struct{} {
	if parent.armed.Load() {
		parent.once.Do(func() { close(parent.idle) })
	}
	return parent.base.Done()
}

func (parent *gatedCancellationContext) Err() error {
	return parent.base.Err()
}

func (parent *gatedCancellationContext) AfterFunc(callback func()) func() bool {
	parent.workers.Add(1)
	stop := context.AfterFunc(parent.base, func() {
		defer parent.workers.Done()
		<-parent.release
		callback()
	})
	return func() bool {
		if stop() {
			parent.workers.Done()
			return true
		}
		return false
	}
}
