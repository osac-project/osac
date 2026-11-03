package workloadcluster

import (
	"context"
	"log/slog"
	"strconv"

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
func (r *function) ensureWatcher(hubID string) {
	if hubID == "" {
		return
	}
	r.watchersLock.Lock()
	if _, exists := r.watchers[hubID]; exists {
		r.watchersLock.Unlock()
		return
	}
	r.watchers[hubID] = struct{}{}
	r.watchersLock.Unlock()

	go func() {
		defer func() {
			r.watchersLock.Lock()
			delete(r.watchers, hubID)
			r.watchersLock.Unlock()
		}()
		if err := r.startWatcher(hubID); err != nil {
			r.logger.Warn("workload cluster Hub watcher stopped", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
		}
	}()
}

func (r *function) startWatcher(hubID string) error {
	ctx := context.Background()
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
	informer.Run(ctx.Done())
	return nil
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
	ctx := context.Background()
	filter := "this.spec.source_hub_id == " + strconv.Quote(hubID) + " && this.spec.managed_cluster_name == " + strconv.Quote(meta.GetName())
	response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return
	}
	for _, item := range response.GetItems() {
		_, _ = r.workloadClusters.Signal(ctx, privatev1.WorkloadClustersSignalRequest_builder{Id: item.GetId()}.Build())
	}
}

func managedClusterResourceGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: managedClusterGroup, Version: managedClusterVersion, Resource: managedClusterResource}
}

// connection is kept as a narrow accessor so the watcher cannot accidentally
// expose the gRPC connection in logs or condition messages.
func (r *function) connection() *grpc.ClientConn { return r.grpcClient }
