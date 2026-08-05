package provisioning_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/aap"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

var _ = Describe("NewProvider", func() {
	var (
		aapClient *mockAAPClient
		ctx       context.Context
	)

	BeforeEach(func() {
		aapClient = &mockAAPClient{}
		ctx = context.Background()
	})

	Context("AAP provider with both explicit templates and prefix", func() {
		It("should use explicit provision template when set", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("my-custom-provision"))
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("my-custom-provision"))
				return &aap.LaunchJobTemplateResponse{JobID: 100}, nil
			}

			provider, err := provisioning.NewProvider(provisioning.ProviderConfig{
				AAPClient:           aapClient,
				ProvisionTemplate:   "my-custom-provision",
				DeprovisionTemplate: "",
				TemplatePrefix:      "osac",
			})
			Expect(err).NotTo(HaveOccurred())

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			instance.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ComputeInstance"))

			result, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("100"))
		})

		It("should fall back to prefix when explicit template is empty", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-delete-compute-instance"))
				return &aap.Template{ID: 2, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("osac-delete-compute-instance"))
				return &aap.LaunchJobTemplateResponse{JobID: 200}, nil
			}

			provider, err := provisioning.NewProvider(provisioning.ProviderConfig{
				AAPClient:           aapClient,
				ProvisionTemplate:   "my-custom-provision",
				DeprovisionTemplate: "",
				TemplatePrefix:      "osac",
			})
			Expect(err).NotTo(HaveOccurred())

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			instance.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ComputeInstance"))

			result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
			Expect(result.JobID).To(Equal("200"))
		})
	})

	Context("AAP provider with prefix only (no explicit templates)", func() {
		It("should use static mapping for ClusterOrder Kind", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-create-hosted-cluster"))
				return &aap.Template{ID: 3, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				return &aap.LaunchJobTemplateResponse{JobID: 300}, nil
			}

			provider, err := provisioning.NewProvider(provisioning.ProviderConfig{
				AAPClient:      aapClient,
				TemplatePrefix: "osac",
			})
			Expect(err).NotTo(HaveOccurred())

			order := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-order", Namespace: "default"},
			}
			order.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ClusterOrder"))

			result, err := provider.TriggerProvision(ctx, order)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("300"))
		})
	})

	Context("AAP provider with explicit templates only (no prefix)", func() {
		It("should use explicit templates", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 4, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("my-provision"))
				return &aap.LaunchJobTemplateResponse{JobID: 400}, nil
			}

			provider, err := provisioning.NewProvider(provisioning.ProviderConfig{
				AAPClient:           aapClient,
				ProvisionTemplate:   "my-provision",
				DeprovisionTemplate: "my-deprovision",
			})
			Expect(err).NotTo(HaveOccurred())

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			instance.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ComputeInstance"))

			result, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("400"))
		})
	})

	Context("AAP provider with no templates and no prefix", func() {
		It("should return error on trigger", func() {
			provider, err := provisioning.NewProvider(provisioning.ProviderConfig{
				AAPClient: aapClient,
			})
			Expect(err).NotTo(HaveOccurred())

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			instance.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ComputeInstance"))

			_, err = provider.TriggerProvision(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("template not configured"))
		})
	})
})
