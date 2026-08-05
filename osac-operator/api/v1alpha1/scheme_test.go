package v1alpha1_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"

	v1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("Scheme Registration", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("GroupVersion", func() {
		It("should have the correct Group", func() {
			Expect(v1alpha1.GroupVersion.Group).To(Equal("osac.openshift.io"))
		})

		It("should have the correct Version", func() {
			Expect(v1alpha1.GroupVersion.Version).To(Equal("v1alpha1"))
		})
	})

	Describe("Kind helper", func() {
		It("should return a GroupKind with the correct group", func() {
			gk := v1alpha1.Kind("ClusterOrder")
			Expect(gk.Group).To(Equal("osac.openshift.io"))
			Expect(gk.Kind).To(Equal("ClusterOrder"))
		})
	})

	Describe("Resource helper", func() {
		It("should return a GroupResource with the correct group", func() {
			gr := v1alpha1.Resource("clusterorders")
			Expect(gr.Group).To(Equal("osac.openshift.io"))
			Expect(gr.Resource).To(Equal("clusterorders"))
		})
	})

	Describe("All registered types", func() {
		type typeEntry struct {
			kind     string
			listKind string
			obj      runtime.Object
			listObj  runtime.Object
		}

		entries := []typeEntry{
			{"ClusterOrder", "ClusterOrderList", &v1alpha1.ClusterOrder{}, &v1alpha1.ClusterOrderList{}},
			{"ComputeInstance", "ComputeInstanceList", &v1alpha1.ComputeInstance{}, &v1alpha1.ComputeInstanceList{}},
			{"Tenant", "TenantList", &v1alpha1.Tenant{}, &v1alpha1.TenantList{}},
			{"Subnet", "SubnetList", &v1alpha1.Subnet{}, &v1alpha1.SubnetList{}},
			{"VirtualNetwork", "VirtualNetworkList", &v1alpha1.VirtualNetwork{}, &v1alpha1.VirtualNetworkList{}},
			{"SecurityGroup", "SecurityGroupList", &v1alpha1.SecurityGroup{}, &v1alpha1.SecurityGroupList{}},
			{"ExternalIPPool", "ExternalIPPoolList", &v1alpha1.ExternalIPPool{}, &v1alpha1.ExternalIPPoolList{}},
			{"ExternalIP", "ExternalIPList", &v1alpha1.ExternalIP{}, &v1alpha1.ExternalIPList{}},
			{"ExternalIPAttachment", "ExternalIPAttachmentList", &v1alpha1.ExternalIPAttachment{}, &v1alpha1.ExternalIPAttachmentList{}},
			{"NATGateway", "NATGatewayList", &v1alpha1.NATGateway{}, &v1alpha1.NATGatewayList{}},
		}

		for _, e := range entries {
			e := e // capture range variable

			It("should register "+e.kind, func() {
				gvk := v1alpha1.GroupVersion.WithKind(e.kind)
				obj, err := scheme.New(gvk)
				Expect(err).NotTo(HaveOccurred())
				Expect(obj).To(BeAssignableToTypeOf(e.obj))
			})

			It("should register "+e.listKind, func() {
				gvk := v1alpha1.GroupVersion.WithKind(e.listKind)
				obj, err := scheme.New(gvk)
				Expect(err).NotTo(HaveOccurred())
				Expect(obj).To(BeAssignableToTypeOf(e.listObj))
			})
		}

		It("should have exactly 20 registered types plus internal types", func() {
			// Verify all 20 expected GVKs are known
			expectedKinds := []string{
				"ClusterOrder", "ClusterOrderList",
				"ComputeInstance", "ComputeInstanceList",
				"Tenant", "TenantList",
				"Subnet", "SubnetList",
				"VirtualNetwork", "VirtualNetworkList",
				"SecurityGroup", "SecurityGroupList",
				"ExternalIPPool", "ExternalIPPoolList",
				"ExternalIP", "ExternalIPList",
				"ExternalIPAttachment", "ExternalIPAttachmentList",
				"NATGateway", "NATGatewayList",
			}
			for _, kind := range expectedKinds {
				gvk := v1alpha1.GroupVersion.WithKind(kind)
				knownTypes := scheme.KnownTypes(v1alpha1.GroupVersion)
				_, found := knownTypes[gvk.Kind]
				Expect(found).To(BeTrue(), "expected kind %s to be registered", kind)
			}
		})
	})

})
