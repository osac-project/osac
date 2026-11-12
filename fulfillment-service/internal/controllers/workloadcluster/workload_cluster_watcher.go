package workloadcluster

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/grpc"
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
func (r *function) ensureWatcher(parent context.Context, hubID string) {
	if hubID == "" {
		return
	}
	r.watchersLock.Lock()
	if _, exists := r.watchers[hubID]; exists {
		r.watchersLock.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.watchers[hubID] = cancel
	r.readyHubs[hubID] = false
	r.watchersLock.Unlock()

	go func() {
		defer func() {
			r.watchersLock.Lock()
			r.readyHubs[hubID] = false
			if cancel := r.watchers[hubID]; cancel != nil {
				cancel()
			}
			delete(r.watchers, hubID)
			r.watchersLock.Unlock()
		}()
		if err := r.startWatcher(ctx, hubID); err != nil && ctx.Err() == nil {
			if r.metrics != nil {
				r.metrics.HubWatchErrors.WithLabelValues(hubID).Inc()
			}
			r.logger.Warn("workload cluster Hub watcher stopped", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
		}
	}()
}

func (r *function) startWatcher(ctx context.Context, hubID string) error {
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
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, nil)
	informer := factory.ForResource(managedClusterResourceGVR()).Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object interface{}) { r.signalForEvent(hubID, object) },
		UpdateFunc: func(_, object interface{}) { r.signalForEvent(hubID, object) },
		DeleteFunc: func(object interface{}) { r.signalForEvent(hubID, object) },
	})
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return errors.New("hub ManagedCluster informer did not sync")
	}
	r.watchersLock.Lock()
	r.readyHubs[hubID] = true
	r.watchersLock.Unlock()
	r.signalForHub(ctx, hubID, "HubReconnected", "Normal")
	<-ctx.Done()
	return nil
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
	response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		r.logger.Warn("Failed to find workload clusters for Hub event", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListFailed"))
		return
	}
	for _, item := range response.GetItems() {
		r.signalWorkloadCluster(ctx, item.GetId(), "", "")
	}
}

func (r *function) signalForHub(ctx context.Context, hubID, reason, severity string) {
	filter := "this.spec.source_hub_id == " + strconv.Quote(hubID)
	response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return
	}
	for _, item := range response.GetItems() {
		r.signalWorkloadCluster(ctx, item.GetId(), reason, severity)
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
