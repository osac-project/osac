// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"time"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ControllerOptions runs one request at a time within each controller.
// Failed requests retry after an increasing delay, from one to five seconds,
// so temporary API errors keep retrying without flooding the API server.
func ControllerOptions() controller.Options {
	return controller.Options{
		MaxConcurrentReconciles: 1,
		RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
			time.Second,
			5*time.Second,
		),
	}
}
