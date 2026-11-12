// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	ignitiontypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
			Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
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

		Expect(c.List(ctx, &secrets, client.MatchingLabels{kube.ManagedByLabel: kube.ManagerName})).To(Succeed())
		Expect(secrets.Items).To(BeEmpty(), "missing inputs must not produce a token or bootstrap output")
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

	DescribeTable(
		"copies only native SSH inputs before publishing the HCP reference",
		func(ctx SpecContext, variant string) {
			hc := agentHostedCluster()
			hc.Spec.SSHKey.Name = "source-ssh"
			source := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.SSHKey.Name},
				Data:       map[string][]byte{"id_rsa.pub": []byte("synthetic public key")},
			}
			pull := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}}
			destination := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "hub-cluster", Name: "ssh-key", UID: "original-copy",
					Annotations:     map[string]string{"retained": "yes"},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Secret", Name: "other", UID: "other-owner"}},
				},
				Data: map[string][]byte{"retained": []byte("other field")},
			}

			switch variant {
			case "unset":
				hc.Spec.SSHKey.Name = ""
			case "missing source":
				source.Name = "unrelated"
			case "missing key":
				source.Data = nil
			case "empty key":
				source.Data["id_rsa.pub"] = nil
			}

			objects := []client.Object{hc, source, pull}
			if variant == "existing destination" {
				objects = append(objects, destination)
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c}

			cp, err := r.inputs(ctx, hc)
			if variant == "missing source" || variant == "missing key" {
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "hub-cluster", Name: hc.Name},
					&hypershiftv1.HostedControlPlane{}))).To(BeTrue())
				return
			}

			Expect(err).NotTo(HaveOccurred())

			key := client.ObjectKey{Namespace: cp.Namespace, Name: "ssh-key"}
			copied := &corev1.Secret{}

			if variant == "unset" {
				Expect(cp.Spec.SSHKey.Name).To(BeEmpty())
				Expect(apierrors.IsNotFound(c.Get(ctx, key, copied))).To(BeTrue())
				return
			}

			Expect(cp.Spec.SSHKey.Name).To(Equal("ssh-key"))
			Expect(c.Get(ctx, key, copied)).To(Succeed())
			Expect(copied.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(copied.Data).To(HaveKey("id_rsa.pub"))
			Expect(bytes.Equal(copied.Data["id_rsa.pub"], source.Data["id_rsa.pub"])).To(BeTrue())
			Expect(copied.Annotations[kube.HostedClusterAnnotation]).To(Equal("hub/cluster"))

			if variant == "existing destination" {
				Expect(copied.UID).To(Equal(destination.UID))
				Expect(copied.OwnerReferences).To(Equal(destination.OwnerReferences))
				Expect(copied.Annotations["retained"]).To(Equal("yes"))
				Expect(copied.Data).To(HaveKey("retained"))
			}

			before := copied.DeepCopy()
			_, err = r.inputs(ctx, hc)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, key, copied)).To(Succeed())
			Expect(copied).To(Equal(before))
		},
		Entry("no SSH reference", "unset"),
		Entry("missing source", "missing source"),
		Entry("missing public-key field", "missing key"),
		Entry("an empty public-key field", "empty key"),
		Entry("an existing destination with foreign fields", "existing destination"),
		Entry("a public-key field", "valid"),
	)

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
