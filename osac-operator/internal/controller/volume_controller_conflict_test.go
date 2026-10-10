package controller

import (
	"context"
	"fmt"
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// conflictOnceStatusClient injects the finalizer/status resource-version race
// observed between the volume and volume-feedback controllers. The first
// status write conflicts; later writes use the real client.
type conflictOnceStatusClient struct {
	client.Client
	statusUpdates  atomic.Int32
	beforeConflict func(context.Context, client.Object)
}

func (c *conflictOnceStatusClient) Status() client.SubResourceWriter {
	return conflictOnceStatusWriter{
		SubResourceWriter: c.Client.Status(),
		client:            c,
	}
}

type conflictOnceStatusWriter struct {
	client.SubResourceWriter
	client *conflictOnceStatusClient
}

func (w conflictOnceStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if w.client.statusUpdates.Add(1) == 1 {
		if w.client.beforeConflict != nil {
			w.client.beforeConflict(ctx, obj)
		}
		return apierrors.NewConflict(
			schema.GroupResource{Group: "osac.openshift.io", Resource: "volumes"},
			obj.GetName(),
			fmt.Errorf("the object has been modified"),
		)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

type staleVolumeClient struct {
	client.Client
	volume *v1alpha1.Volume
}

func (c *staleVolumeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if vol, ok := obj.(*v1alpha1.Volume); ok && key == client.ObjectKeyFromObject(c.volume) {
		c.volume.DeepCopyInto(vol)
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
