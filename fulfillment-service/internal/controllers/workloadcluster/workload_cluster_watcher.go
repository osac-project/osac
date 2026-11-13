package workloadcluster

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

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
			if err := r.startWatcher(ctx, hubID, reconnecting); err == nil || ctx.Err() != nil {
				return
			}
			r.watchersLock.Lock()
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

func (r *function) startWatcher(ctx context.Context, hubID string, reconnecting bool) error {
	hubResponse, err := privatev1.NewHubsClient(r.connection()).Get(ctx, privatev1.HubsGetRequest_builder{Id: hubID}.Build())
	if err != nil {
		return err
	}
	secrets := privatev1.NewSecretsClient(r.connection())
	kubeconfig, err := controllers.ResolveHubKubeconfig(ctx, hubResponse.GetObject().GetSpec(), func(ctx context.Context, id string) (*privatev1.Secret, error) {
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
	if err := informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		r.watchersLock.Lock()
		r.readyHubs[hubID] = false
		r.watchersLock.Unlock()
		if r.metrics != nil {
			r.metrics.HubWatchErrors.WithLabelValues(hubID).Inc()
		}
		r.logger.Warn("workload cluster Hub watch failed", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
		go r.signalHubUnavailable(hubID)
		cancel()
	}); err != nil {
		return err
	}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object interface{}) { r.signalForEvent(hubID, object) },
		UpdateFunc: func(_, object interface{}) { r.signalForEvent(hubID, object) },
		DeleteFunc: func(object interface{}) { r.signalForEvent(hubID, object) },
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
	if reconnecting {
		r.signalForHub(ctx, hubID, "HubReconnected", "Normal")
	} else {
		// Initial cache sync must trigger reconciliation without being reported as
		// a reconnect event. A signal without metadata is an internal wake-up.
		r.signalForHub(ctx, hubID, "", "")
	}
	<-watchCtx.Done()
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("hub ManagedCluster watch stopped")
}

func (r *function) signalHubUnavailable(hubID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func (r *function) signalForEvent(hubID string, object interface{}) {
	meta, ok := object.(metav1.Object)
	if !ok || meta.GetName() == "" {
		if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
			meta, _ = tombstone.Obj.(metav1.Object)
		}
	}
	if meta == nil || meta.GetName() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filter := "this.spec.source_hub_id == " + strconv.Quote(hubID) + " && this.spec.managed_cluster_name == " + strconv.Quote(meta.GetName())
	for offset := int32(0); ; offset += 100 {
		response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Offset: &offset, Limit: proto.Int32(100)}.Build())
		if err != nil {
			r.logger.Warn("Failed to find workload clusters for Hub event", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListFailed"))
			return
		}
		for _, item := range response.GetItems() {
			r.signalWorkloadCluster(ctx, item.GetId(), "", "")
		}
		if len(response.GetItems()) == 0 || int64(offset)+int64(len(response.GetItems())) >= int64(response.GetTotal()) {
			return
		}
	}
}

func (r *function) signalForHub(ctx context.Context, hubID, reason, severity string) {
	filter := "this.spec.source_hub_id == " + strconv.Quote(hubID)
	for offset := int32(0); ; offset += 100 {
		response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Offset: &offset, Limit: proto.Int32(100)}.Build())
		if err != nil {
			return
		}
		for _, item := range response.GetItems() {
			r.signalWorkloadCluster(ctx, item.GetId(), reason, severity)
		}
		if len(response.GetItems()) == 0 || int64(offset)+int64(len(response.GetItems())) >= int64(response.GetTotal()) {
			return
		}
	}
}

func (r *function) signalWorkloadCluster(ctx context.Context, id, reason, severity string) {
	var signalErr error
	for attempt := 0; attempt < 3; attempt++ {
		_, signalErr = r.workloadClusters.Signal(ctx, privatev1.WorkloadClustersSignalRequest_builder{
			Id: id, Reason: reason, Severity: severity,
		}.Build())
		if signalErr == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if signalErr != nil {
		r.logger.Warn("Failed to signal workload cluster", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "SignalFailed"))
	}
}

func managedClusterResourceGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: managedClusterGroup, Version: managedClusterVersion, Resource: managedClusterResource}
}

// connection is kept as a narrow accessor so the watcher cannot accidentally
// expose the gRPC connection in logs or condition messages.
func (r *function) connection() *grpc.ClientConn { return r.grpcClient }
