/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// QuotaDefaultLimitScanner writes the threshold warnings a default limit change leaves behind.
// A default belongs to every tenant, so the change has no request that could scan them all: the
// server schedules the scan after the change commits, and this worker runs it detached from that
// request. One worker serializes every scan, requests coalesce per dimension, and a newer change
// of a dimension replaces an older pending one: two rapid changes of one dimension scan once,
// against the newest state.
//
// A scan lists the tenants in one transaction and then warns each tenant in a transaction of its
// own. A tenant that fails, or that is deleted before its turn, is logged and skipped; the walk
// continues with the remaining tenants.
type QuotaDefaultLimitScanner struct {
	logger  *slog.Logger
	manager database.TxManager

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mutex   sync.Mutex
	pending map[string]pendingDefaultScan
	started bool
}

// pendingDefaultScan is one queued change: the enforcer that knows the tenant's state and the
// default the change replaces.
type pendingDefaultScan struct {
	enforcer   *quotaEnforcer
	oldDefault int64
}

// QuotaDefaultLimitScannerBuilder configures a scanner.
type QuotaDefaultLimitScannerBuilder struct {
	logger  *slog.Logger
	manager database.TxManager
}

// NewQuotaDefaultLimitScanner creates a builder for a scanner. The worker starts when the
// scanner is built and stops with Close.
func NewQuotaDefaultLimitScanner() *QuotaDefaultLimitScannerBuilder {
	return &QuotaDefaultLimitScannerBuilder{}
}

// SetLogger sets the logger. It is mandatory.
func (b *QuotaDefaultLimitScannerBuilder) SetLogger(value *slog.Logger) *QuotaDefaultLimitScannerBuilder {
	b.logger = value
	return b
}

// SetTxManager sets the transaction manager the scans use. It is mandatory.
func (b *QuotaDefaultLimitScannerBuilder) SetTxManager(value database.TxManager) *QuotaDefaultLimitScannerBuilder {
	b.manager = value
	return b
}

// Build creates the scanner and starts its worker.
func (b *QuotaDefaultLimitScannerBuilder) Build() (*QuotaDefaultLimitScanner, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.manager == nil {
		return nil, errors.New("transaction manager is mandatory")
	}
	result := newQuotaDefaultLimitScanner(b.logger, b.manager)
	result.start()
	return result, nil
}

// newQuotaDefaultLimitScanner creates a scanner without starting its worker. The specs drive
// drain directly; Build starts the worker production uses.
func newQuotaDefaultLimitScanner(
	logger *slog.Logger, manager database.TxManager,
) *QuotaDefaultLimitScanner {
	ctx, cancel := context.WithCancel(context.Background())
	return &QuotaDefaultLimitScanner{
		logger:  logger,
		manager: manager,
		ctx:     ctx,
		cancel:  cancel,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		pending: map[string]pendingDefaultScan{},
	}
}

// start runs the worker. It is called once, by Build.
func (s *QuotaDefaultLimitScanner) start() {
	s.started = true
	go s.run()
}

// Close stops the worker and waits for it to finish. Scans that are still running fail their
// queries with the canceled context, so Close does not wait for a tenant walk to complete.
func (s *QuotaDefaultLimitScanner) Close() error {
	if s == nil || !s.started {
		return nil
	}
	s.cancel()
	<-s.done
	return nil
}

// schedule queues one change. A newer change of the same dimension replaces the pending one, so
// a burst of changes scans each dimension once.
func (s *QuotaDefaultLimitScanner) schedule(
	enforcer *quotaEnforcer, dimension string, oldDefault int64,
) {
	if s == nil {
		return
	}
	s.mutex.Lock()
	s.pending[dimension] = pendingDefaultScan{enforcer: enforcer, oldDefault: oldDefault}
	s.mutex.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run drains the queued changes until Close cancels the worker.
func (s *QuotaDefaultLimitScanner) run() {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
			s.drain()
		}
	}
}

// drain takes the pending changes and scans each dimension once. A change scheduled while a
// scan runs is left for the next wake, because it describes a state after the scan's snapshot.
func (s *QuotaDefaultLimitScanner) drain() {
	s.mutex.Lock()
	pending := s.pending
	s.pending = map[string]pendingDefaultScan{}
	s.mutex.Unlock()
	for dimension, scan := range pending {
		s.scanDimension(dimension, scan)
	}
}

// scanDimension lists the tenants and warns each one. The tenant list is read in a transaction
// of its own, so the per-tenant transactions start from one committed snapshot.
func (s *QuotaDefaultLimitScanner) scanDimension(dimension string, scan pendingDefaultScan) {
	tx, err := s.manager.Begin(s.ctx)
	if err != nil {
		s.logger.ErrorContext(s.ctx, "Failed to scan the tenants after a default limit change",
			slog.String("dimension", dimension),
			slog.Any("error", err))
		return
	}
	tenantCtx := database.TxIntoContext(s.ctx, tx)
	tenants, err := scan.enforcer.listTenants(tenantCtx)
	if endErr := tx.End(s.ctx); endErr != nil || err != nil {
		s.logger.ErrorContext(s.ctx, "Failed to scan the tenants after a default limit change",
			slog.String("dimension", dimension),
			slog.Any("error", errors.Join(err, endErr)))
		return
	}
	s.scanTenants(tenants, dimension, scan)
}

// scanTenants warns one tenant at a time, each in its own transaction. A tenant whose
// transaction fails is logged and skipped, so one bad or concurrently deleted tenant never
// stops the remaining walk.
func (s *QuotaDefaultLimitScanner) scanTenants(
	tenants []string, dimension string, scan pendingDefaultScan,
) {
	for _, tenant := range tenants {
		if err := s.scanTenant(tenant, dimension, scan); err != nil {
			s.logger.ErrorContext(s.ctx, "Failed to write the quota warnings of a tenant",
				slog.String("tenant", tenant),
				slog.String("dimension", dimension),
				slog.Any("error", err))
		}
	}
}

// scanTenant runs one tenant's scan in its own transaction.
func (s *QuotaDefaultLimitScanner) scanTenant(
	tenant, dimension string, scan pendingDefaultScan,
) error {
	tx, err := s.manager.Begin(s.ctx)
	if err != nil {
		return err
	}
	tenantCtx := database.TxIntoContext(s.ctx, tx)
	// The tenant's row locks before the warning counts its usage, so a warning the scan
	// writes is measured against a state no admission can move while the scan runs. A
	// tenant being deleted refuses the lock; the walk logs it and continues.
	if lockErr := lockTenantRowForCleanup(tenantCtx, scan.enforcer.metrics, tenant); lockErr != nil {
		return errors.Join(lockErr, tx.End(s.ctx))
	}
	warnErr := scan.enforcer.warnTenantAfterDefaultChange(
		tenantCtx, tenant, dimension, scan.oldDefault,
	)
	return errors.Join(warnErr, tx.End(s.ctx))
}
