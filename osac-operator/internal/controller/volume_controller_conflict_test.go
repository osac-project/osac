package controller

import (
	"context"
	"fmt"
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// conflictOnceStatusClient injects the finalizer/status resource-version race
// observed between the volume and volume-feedback controllers. The first
// status write conflicts; later writes use the real client.
type conflictOnceStatusClient struct {
	client.Client
	statusUpdates atomic.Int32
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
		return apierrors.NewConflict(
			schema.GroupResource{Group: "osac.openshift.io", Resource: "volumes"},
			obj.GetName(),
			fmt.Errorf("the object has been modified"),
		)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
