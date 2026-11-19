package workloadcluster

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type managedClusterSignal struct {
	hubID string
	name  string
}

// ensureWatcher starts one informer per referenced Hub. The informer is shared
// by all WorkloadClusters registered against that Hub and only emits opaque
// resource IDs through the private Signal RPC.
func (r *function) ensureWatcher(parent context.Context, hubID, credentialFingerprint string) {
	if hubID == "" {
		return
	}
	r.watchersLock.Lock()
	if current, exists := r.watchers[hubID]; exists {
		if credentialFingerprint == "" || r.watcherCredentials[hubID] == credentialFingerprint {
			r.watchersLock.Unlock()
			return
		}
		// The Hub credential changed. Stop the old informer and let the new
		// watcher use the refreshed credential from the next start.
		current()
		delete(r.watchers, hubID)
		delete(r.watcherCredentials, hubID)
	}
	ctx, cancel := context.WithCancel(parent)
	r.watchers[hubID] = cancel
	r.watcherCredentials[hubID] = credentialFingerprint
	r.watcherGenerations[hubID]++
	generation := r.watcherGenerations[hubID]
	r.readyHubs[hubID] = false
	r.watchEstablished[hubID] = false
	r.watchersLock.Unlock()

	go func() {
		reconnecting := false
		defer func() {
			r.watchersLock.Lock()
			if r.watcherGenerations[hubID] == generation {
				r.readyHubs[hubID] = false
				cancel()
				delete(r.watchers, hubID)
				delete(r.watcherCredentials, hubID)
				delete(r.watchEstablished, hubID)
			}
			r.watchersLock.Unlock()
		}()
		for ctx.Err() == nil {
			if err := r.startWatcher(ctx, hubID, generation, reconnecting); err == nil || ctx.Err() != nil {
				return
			}
			r.watchersLock.Lock()
			if r.watcherGenerations[hubID] != generation {
				r.watchersLock.Unlock()
				return
			}
			reconnecting = r.watchEstablished[hubID]
			r.readyHubs[hubID] = false
			r.watchersLock.Unlock()
			r.logger.Warn("workload cluster Hub watcher stopped", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (r *function) startWatcher(ctx context.Context, hubID string, generation uint64, reconnecting bool) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	hubResponse, err := privatev1.NewHubsClient(r.connection()).Get(startupCtx, privatev1.HubsGetRequest_builder{Id: hubID}.Build())
	if err != nil {
		return err
	}
	secrets := privatev1.NewSecretsClient(r.connection())
	kubeconfig, err := controllers.ResolveHubKubeconfig(startupCtx, hubResponse.GetObject().GetSpec(), func(ctx context.Context, id string) (*privatev1.Secret, error) {
		response, err := secrets.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return nil, err
		}
		return response.GetObject(), nil
	})
	if err != nil {
		return err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, nil)
	informer := factory.ForResource(managedClusterResourceGVR()).Informer()
	signalQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[managedClusterSignal]())
	defer signalQueue.ShutDown()
	for i := 0; i < 4; i++ {
		go r.runManagedClusterSignalWorker(watchCtx, signalQueue)
	}
	if err := informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		r.watchersLock.Lock()
		ownsWatcher := r.watcherGenerations[hubID] == generation
		if ownsWatcher {
			r.readyHubs[hubID] = false
		}
		r.watchersLock.Unlock()
		if ownsWatcher && r.metrics != nil {
			r.metrics.HubWatchErrors.WithLabelValues(hubID).Inc()
		}
		if ownsWatcher {
			r.logger.Warn("workload cluster Hub watch failed", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
			go r.signalHubUnavailable(ctx, hubID)
		}
		cancel()
	}); err != nil {
		return err
	}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
		UpdateFunc: func(_, object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
		DeleteFunc: func(object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
	}); err != nil {
		return err
	}
	factory.Start(watchCtx.Done())
	if !cache.WaitForCacheSync(watchCtx.Done(), informer.HasSynced) {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("hub ManagedCluster informer did not sync")
	}
	r.watchersLock.Lock()
	if r.watcherGenerations[hubID] != generation {
		r.watchersLock.Unlock()
		return nil
	}
	r.readyHubs[hubID] = true
	r.managedCaches[hubID] = informer
	r.watchEstablished[hubID] = true
	r.watchersLock.Unlock()
	defer func() {
		r.watchersLock.Lock()
		if current := r.managedCaches[hubID]; current == informer {
			delete(r.managedCaches, hubID)
		}
		r.watchersLock.Unlock()
	}()
	if reconnecting && r.ownsWatcher(hubID, generation) {
		go r.signalForHub(watchCtx, hubID, "HubReconnected", "Normal")
	} else if r.ownsWatcher(hubID, generation) {
		// Initial cache sync must trigger reconciliation without being reported as
		// a reconnect event. A signal without metadata is an internal wake-up.
		go r.signalForHub(watchCtx, hubID, "", "")
	}
	<-watchCtx.Done()
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("hub ManagedCluster watch stopped")
}

func (r *function) ownsWatcher(hubID string, generation uint64) bool {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.watcherGenerations[hubID] == generation
}

func (r *function) signalHubUnavailable(parent context.Context, hubID string) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	// Use an internal signal without event metadata: this reconciles every
	// registration against the failed Hub without emitting a recovery event.
	r.signalForHub(ctx, hubID, "", "")
}

func (r *function) isHubReady(hubID string) bool {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.readyHubs[hubID]
}

func (r *function) runManagedClusterSignalWorker(ctx context.Context, queue workqueue.TypedRateLimitingInterface[managedClusterSignal]) {
	for {
		item, shutdown := queue.Get()
		if shutdown {
			return
		}
		err := r.signalForEvent(ctx, item.hubID, item.name)
		if err != nil && ctx.Err() == nil {
			queue.AddRateLimited(item)
		} else {
			queue.Forget(item)
		}
		queue.Done(item)
	}
}

func enqueueManagedClusterSignal(queue workqueue.TypedRateLimitingInterface[managedClusterSignal], hubID string, object interface{}) {
	meta, ok := object.(metav1.Object)
	if !ok || meta.GetName() == "" {
		if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
			meta, _ = tombstone.Obj.(metav1.Object)
		}
	}
	if meta == nil || meta.GetName() == "" {
		return
	}
	queue.Add(managedClusterSignal{hubID: hubID, name: meta.GetName()})
}

func (r *function) signalForEvent(parent context.Context, hubID, managedClusterName string) error {
	filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID) + " && this.spec.maas.managed_cluster_name == " + strconv.Quote(managedClusterName)
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Limit: proto.Int32(100)}.Build())
		cancel()
		if err != nil {
			if parent.Err() != nil {
				return parent.Err()
			}
			timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
			select {
			case <-parent.Done():
				timer.Stop()
				return parent.Err()
			case <-timer.C:
			}
			continue
		}
		for _, item := range response.GetItems() {
			if err := r.signalWorkloadCluster(parent, item.GetId(), "", ""); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("failed to list workload clusters for Hub event")
}

func (r *function) signalForHub(ctx context.Context, hubID, reason, severity string) {
	filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID)
	var offset int32
	var ids []string
	for {
		var response *privatev1.WorkloadClustersListResponse
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			response, err = r.workloadClusters.List(listCtx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Offset: &offset, Limit: proto.Int32(100)}.Build())
			cancel()
			if err == nil {
				break
			}
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
		if err != nil {
			r.logger.Warn("Failed to find workload clusters for Hub", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListFailed"))
			return
		}
		for _, item := range response.GetItems() {
			ids = append(ids, item.GetId())
		}
		if len(response.GetItems()) == 0 || int64(offset)+int64(len(response.GetItems())) >= int64(response.GetTotal()) {
			break
		}
		if len(response.GetItems()) > 1<<31-1 {
			r.logger.Warn("Too many workload clusters returned in one page", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListTooLarge"))
			return
		}
		offset += int32(len(response.GetItems()))
	}

	// Enumerate all pages before dispatching signals. A slow retry for one
	// registration must not prevent later pages from being discovered.
	const workerCount = 8
	jobs := make(chan string)
	var signals sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		signals.Add(1)
		go func() {
			defer signals.Done()
			for id := range jobs {
				signalCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				r.retrySignalWorkloadCluster(signalCtx, id, reason, severity)
				cancel()
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			signals.Wait()
			return
		}
	}
	close(jobs)
	signals.Wait()
}

func (r *function) retrySignalWorkloadCluster(ctx context.Context, id, reason, severity string) {
	for {
		if err := r.signalWorkloadCluster(ctx, id, reason, severity); err == nil {
			return
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *function) signalWorkloadCluster(ctx context.Context, id, reason, severity string) error {
	backoff := 250 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, signalErr := r.workloadClusters.Signal(attemptCtx, privatev1.WorkloadClustersSignalRequest_builder{
			Id: id, Reason: reason, Severity: severity,
		}.Build())
		cancel()
		if signalErr == nil || ctx.Err() != nil || isPermanentSignalError(signalErr) {
			if signalErr != nil && ctx.Err() == nil {
				r.logger.Warn("Failed to signal workload cluster", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "SignalFailed"))
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
	r.logger.Warn("Failed to signal workload cluster", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "SignalFailed"))
	return errors.New("failed to signal workload cluster")
}

func isPermanentSignalError(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated:
		return true
	default:
		return false
	}
}

func managedClusterResourceGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: managedClusterGroup, Version: managedClusterVersion, Resource: managedClusterResource}
}

// connection is kept as a narrow accessor so the watcher cannot accidentally
// expose the gRPC connection in logs or condition messages.
func (r *function) connection() *grpc.ClientConn { return r.grpcClient }
