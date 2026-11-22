// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	ignitiontypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// ignitionInputs supplies independently published endpoint and certificate inputs.
// The symbolic certificate tests resource dependencies without making a TLS connection.
func ignitionInputs(pool *hypershiftv1.NodePool, cluster *capiv1.Cluster) []client.Object {
	hc := agentHostedCluster()
	hc.Status.IgnitionEndpoint = "ignition.example.invalid"
	pool.Spec.ClusterName = hc.Name

	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}

	cluster.Annotations[kube.HostedClusterAnnotation] = hc.Namespace + "/" + hc.Name
	cluster.Spec.ControlPlaneRef = &corev1.ObjectReference{
		APIVersion: hypershiftv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       hc.Name,
	}
	cp := &hypershiftv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: hc.Name, UID: "original-control-plane",
			Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
		},
	}
	ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: "ignition-server-ca-cert"},
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("certificate")},
	}
	Expect(controllerutil.SetControllerReference(cp, ca, hypershiftScheme())).To(Succeed())

	return []client.Object{hc, cp, ca}
}

var _ = Describe("Ignition publication inputs", func() {
	DescribeTable("observes native Route hosts without manufacturing an endpoint",
		func(ctx SpecContext, input, previous, expected string) {
			hc := agentHostedCluster()
			hc.Spec.Services[0].Route = nil
			hc.Status.IgnitionEndpoint = previous
			route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "ignition-server"}}

			switch input {
			case "explicit":
				hc.Spec.Services[0].Route = &hypershiftv1.RoutePublishingStrategy{Hostname: "explicit.example.invalid"}
			case "assigned":
				route.Spec.Host = "assigned.apps.example.invalid"
			case "ingress-only":
				route.Status.Ingress = []routev1.RouteIngress{{Host: "ingress.apps.example.invalid"}}
			}

			builder := fake.NewClientBuilder().WithScheme(hypershiftScheme())
			if input != "missing" {
				builder.WithObjects(route)
			}

			c := builder.Build()
			failure := errors.New("Route read failed")
			reader := interceptor.NewClient(c, interceptor.Funcs{Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption,
			) error {
				if input == "error" || input == "explicit" {
					return failure
				}

				return c.Get(ctx, key, object, opts...)
			}})
			r := &HostedClusterReconciler{Client: c, APIReader: reader}

			endpoint, err := r.ignitionEndpoint(ctx, hc)
			if input == "error" {
				Expect(errors.Is(err, failure)).To(BeTrue())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoint).To(Equal(expected))
			}
		},
		Entry("explicit hostname", "explicit", "", "explicit.example.invalid"),
		Entry("API-defaulted hostname", "assigned", "", "assigned.apps.example.invalid"),
		Entry("missing Route", "missing", "", ""),
		Entry("empty Route", "empty", "", ""),
		Entry("ingress cannot replace spec.host", "ingress-only", "", ""),
		Entry("missing observation retains the endpoint", "missing", "previous.example.invalid", "previous.example.invalid"),
		Entry("empty observation retains the endpoint", "empty", "previous.example.invalid", "previous.example.invalid"),
		Entry("read error", "error", "", ""),
	)

	DescribeTable("requires each management-side bootstrap dependency", func(ctx SpecContext, missing string) {
		pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "pool"}}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		objects := ignitionInputs(pool, cluster)

		switch missing {
		case "endpoint":
			objects[0].(*hypershiftv1.HostedCluster).Status.IgnitionEndpoint = ""
		case "CA":
			objects = objects[:2]
		case "certificate":
			objects[2].(*corev1.Secret).Data = nil
		case "CA owner":
			objects[2].SetOwnerReferences(nil)
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		_, err := r.bootstrapData(ctx, pool, cluster)
		Expect(err).To(HaveOccurred())

		var secrets corev1.SecretList

		Expect(c.List(ctx, &secrets)).To(Succeed())

		for _, secret := range secrets.Items {
			Expect(
				secret.Name,
			).To(Equal("ignition-server-ca-cert"), "missing inputs must not produce a token or bootstrap output")
		}

		if missing == "CA" {
			Expect(secrets.Items).To(BeEmpty())
		} else {
			Expect(secrets.Items).To(HaveLen(1))
		}
	},
		Entry("endpoint", "endpoint"),
		Entry("CA Secret", "CA"),
		Entry("CA contents", "certificate"),
		Entry("original CA owner", "CA owner"),
	)

	It("builds authenticated Ignition from the endpoint, certificate and stable native token", func(ctx SpecContext) {
		pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "pool"}}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(ignitionInputs(pool, cluster)...).Build()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		data, err := r.bootstrapData(ctx, pool, cluster)
		Expect(err).NotTo(HaveOccurred())

		var config ignitiontypes.Config

		Expect(json.Unmarshal(data, &config)).To(Succeed())
		Expect(config.Ignition.Config.Merge).To(HaveLen(1))
		Expect(config.Ignition.Config.Merge[0].Source).To(Equal(new("https://ignition.example.invalid/ignition")))

		token := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: "workers-token"}, token)).To(Succeed())
		Expect(token.Data["token"]).NotTo(BeEmpty())
		Expect(
			config.Ignition.Config.Merge[0].HTTPHeaders[0].Value,
		).To(Equal(new("Bearer " + base64.StdEncoding.EncodeToString(token.Data["token"]))))
		Expect(
			config.Ignition.Security.TLS.CertificateAuthorities[0].Source,
		).To(Equal(new("data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("certificate")))))

		restarted := &NodePoolReconciler{Client: c, APIReader: c}
		recovered, err := restarted.bootstrapData(ctx, pool, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered).To(Equal(data))
	})

	DescribeTable("observes independently allocated ignition ports", func(ctx SpecContext, name string, allocated int32) {
		hc := agentHostedCluster()
		hc.Spec.Services[0].Type = hypershiftv1.NodePort
		hc.Spec.Services[0].NodePort = &hypershiftv1.NodePortPublishingStrategy{Address: "192.0.2.5"}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: name},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 443, NodePort: allocated}}},
		}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, service).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c}
		endpoint, err := r.ignitionEndpoint(ctx, hc)
		Expect(err).NotTo(HaveOccurred())

		if allocated == 0 {
			Expect(endpoint).To(BeEmpty(), "publication must wait for API-server allocation")
		} else {
			Expect(endpoint).To(Equal(fmt.Sprintf("192.0.2.5:%d", allocated)))
		}
	},
		Entry("an unallocated proxy port", "ignition-server-proxy", int32(0)),
		Entry("an allocated proxy port", "ignition-server-proxy", int32(30443)),
		Entry("the legacy backend Service", "ignition-server", int32(30443)),
	)
})
