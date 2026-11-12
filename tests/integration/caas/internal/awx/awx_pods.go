// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// JobPodObserver keeps copies of the AWX worker Pods seen during a test.
//
// AWX can remove a Pod soon after its job finishes. Saving a copy lets the test
// check which image actually ran even after the Pod is gone. A Pod is saved
// after Kubernetes reports an image ID for its worker container.
//
// Start observing before OSAC launches jobs, and keep the observer running
// through job checks and cleanup. Close stops the background observation loop.
type JobPodObserver struct {
	mu   sync.Mutex
	pods map[string]corev1.Pod
	err  error
	done chan struct{}
	stop context.CancelFunc
}

// ObserveJobPods establishes a list/watch and waits for initial synchronization
// before returning. Events save Pod copies with worker image IDs, including a Pod's
// final deletion event. Close ends observation after resource cleanup.
func ObserveJobPods(ctx context.Context, e *harness.Environment) (*JobPodObserver, error) {
	startup, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()

	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	x := &JobPodObserver{pods: make(map[string]corev1.Pod), done: make(chan struct{}), stop: cancel}
	pods := e.WatchKube.CoreV1().Pods(e.Namespace)
	informer := cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			options.LabelSelector = "ansible-awx-job-id"

			return pods.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = "ansible-awx-job-id"
			return pods.Watch(ctx, options)
		},
	}, e.WatchKube), &corev1.Pod{}, 0, cache.Indexers{})
	save := func(object any) {
		if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
			object = tombstone.Obj
		}

		if pod, ok := object.(*corev1.Pod); ok {
			x.observe([]corev1.Pod{*pod}, nil)
		}
	}

	registration, err := informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{AddFunc: save, UpdateFunc: func(_, current any) { save(current) }, DeleteFunc: save},
	)
	if err != nil {
		cancel()
		return nil, err
	}

	if err = informer.SetWatchErrorHandlerWithContext(
		func(_ context.Context, _ *cache.Reflector, err error) { x.observe(nil, err) },
	); err != nil {
		cancel()
		return nil, err
	}

	go func() { defer close(x.done); informer.RunWithContext(lifetime) }()

	if !cache.WaitForCacheSync(startup.Done(), registration.HasSynced) {
		x.Close()
		return nil, fmt.Errorf("execution Pod observer did not synchronize: %w", startup.Err())
	}

	slog.DebugContext(ctx, "AWX execution Pod observer synchronized", "namespace", e.Namespace)

	return x, nil
}

// observe saves independent copies of Pods with a worker image ID and remembers
// the latest watch error. Existing copies stay available during API failures.
func (x *JobPodObserver) observe(pods []corev1.Pod, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.err = err
	if err != nil {
		slog.Debug("AWX execution Pod watch failed; retaining captured evidence")
	}

	// A failed read or a deleted Pod leaves earlier snapshots available. Save
	// independent copies so later Kubernetes updates cannot change that evidence.
	for _, pod := range pods {
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "worker" && status.ImageID != "" {
				if previous, exists := x.pods[pod.Labels["ansible-awx-job-id"]]; !exists || previous.UID != pod.UID {
					slog.Debug("retaining AWX execution Pod evidence",
						"namespace", pod.Namespace,
						"pod", pod.Name,
						"podUID", pod.UID,
						"job", pod.Labels["ansible-awx-job-id"],
						"node", pod.Spec.NodeName,
					)
				}

				x.pods[pod.Labels["ansible-awx-job-id"]] = *pod.DeepCopy()
			}
		}
	}
}

// Close stops observation and waits for its API request to finish.
func (x *JobPodObserver) Close() {
	x.stop()
	<-x.done
	slog.Debug("AWX execution Pod observer stopped")
}

// podForJob returns a copy of the saved worker Pod for this AWX job.
// Saved Pods remain available after deletion and during temporary API failures.
// An error means a usable snapshot has not been captured for this job.
func (x *JobPodObserver) podForJob(id string) (*corev1.Pod, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if pod, exists := x.pods[id]; exists {
		return pod.DeepCopy(), nil
	}

	if x.err != nil {
		return nil, fmt.Errorf("observing execution Pods: %w", x.err)
	}

	return nil, fmt.Errorf("job %s has no observed execution Pod", id)
}
