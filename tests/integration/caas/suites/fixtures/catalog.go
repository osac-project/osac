// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

const ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"

// EnsureClusterCatalog creates the reusable catalog entries, pull secret and
// cluster template needed by local cluster requests. Catalog entries remain
// available across tests; images and versions are shared, while Secrets and the
// cluster template belong to tenant.
// OSAC creates tenant default networking; the Tenant fixture waits for it separately.
func EnsureClusterCatalog(
	ctx context.Context,
	admin, user *grpc.ClientConn,
	tenant string,
	groups []InventoryGroup,
) error {
	slog.InfoContext(ctx, "preparing reusable cluster catalog", "tenant", tenant)

	if err := ensureWorkerRelease(ctx, admin); err != nil {
		return err
	}

	// Each instance type selects only its group's original hardware.
	if err := ensureGroupInstanceTypes(ctx, admin, groups); err != nil {
		return err
	}

	// Register the production bare-metal template used by worker provisioning.
	bmTemplates := privatev1.NewBareMetalInstanceTemplatesClient(admin)

	bmTemplate, err := ensure(func() ([]*privatev1.BareMetalInstanceTemplate, error) {
		r, err := bmTemplates.List(
			ctx,
			privatev1.BareMetalInstanceTemplatesListRequest_builder{Filter: catalogFilter("caas-it-bm-host", "shared")}.Build(),
		)

		return r.GetItems(), err
	}, func() (*privatev1.BareMetalInstanceTemplate, error) {
		r, err := bmTemplates.Create(
			ctx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "osac.templates.bm_host_provisioning",
				Metadata:    catalogMetadata("caas-it-bm-host", "shared"),
				Title:       "Bare Metal Host Image Provisioning",
				Description: "Production bm_host_provisioning template",
			}.Build()}.Build(),
		)

		return r.GetObject(), err
	})
	if err != nil {
		return err
	}

	if bmTemplate.GetId() != "osac.templates.bm_host_provisioning" {
		return fmt.Errorf("caas-it-bm-host selects a different template")
	}

	return ensureClusterTemplate(ctx, admin, user, tenant)
}

// ensureWorkerRelease shares one release and its Metal3-compatible worker image.
// Cluster versions are platform catalog entries and must belong to the shared tenant.
func ensureWorkerRelease(ctx context.Context, admin *grpc.ClientConn) error {
	const (
		imageName   = "caas-it-worker-image"
		source      = "oci://quay.io/containerdisks/fedora:42"
		versionName = "caas-it-4-17-0"
	)

	images := privatev1.NewDiskImagesClient(admin)

	image, err := ensure(func() ([]*privatev1.DiskImage, error) {
		r, err := images.List(
			ctx,
			privatev1.DiskImagesListRequest_builder{Filter: catalogFilter(imageName, "shared")}.Build(),
		)

		return r.GetItems(), err
	}, func() (*privatev1.DiskImage, error) {
		r, err := images.Create(ctx, privatev1.DiskImagesCreateRequest_builder{Object: privatev1.DiskImage_builder{
			Metadata: catalogMetadata(imageName, "shared"),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     source,
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
				Lifecycle:     privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
			}.Build(),
		}.Build()}.Build())

		return r.GetObject(), err
	})
	if err != nil {
		return err
	}

	imageSpec := image.GetSpec()
	if image.GetMetadata().GetTenant() != "shared" ||
		imageSpec.GetSourceType() != privatev1.SourceType_SOURCE_TYPE_REGISTRY ||
		imageSpec.GetSourceRef() != source ||
		imageSpec.GetGuestOsFamily() != privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX ||
		!slices.Contains(imageSpec.GetArchitecture(), privatev1.Architecture_ARCHITECTURE_AMD64) ||
		imageSpec.GetLifecycle() != privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE {
		return fmt.Errorf("%s has incompatible ownership, source, architecture or lifecycle", imageName)
	}

	versions := privatev1.NewClusterVersionsClient(admin)

	version, err := ensure(func() ([]*privatev1.ClusterVersion, error) {
		r, err := versions.List(
			ctx,
			privatev1.ClusterVersionsListRequest_builder{Filter: catalogFilter(versionName, "shared")}.Build(),
		)

		return r.GetItems(), err
	}, func() (*privatev1.ClusterVersion, error) {
		r, err := versions.Create(
			ctx,
			privatev1.ClusterVersionsCreateRequest_builder{Object: privatev1.ClusterVersion_builder{
				Metadata: catalogMetadata(versionName, "shared"),
				Spec: privatev1.ClusterVersionSpec_builder{
					Version:   "4.17.0",
					Image:     ReleaseImage,
					DiskImage: privatev1.DiskImageReference_builder{Name: imageName, Shared: true}.Build(),
				}.Build(),
			}.Build()}.Build(),
		)

		return r.GetObject(), err
	})
	if err != nil {
		return err
	}

	if version.GetMetadata().GetTenant() != "shared" ||
		version.GetSpec().GetVersion() != "4.17.0" ||
		version.GetSpec().GetImage() != ReleaseImage ||
		version.GetSpec().GetDiskImage().GetName() != imageName ||
		!version.GetSpec().GetDiskImage().GetShared() {
		return fmt.Errorf("%s has incompatible ownership, release or worker image", versionName)
	}

	return nil
}

// ensureClusterTemplate prepares tenant-owned credentials and the production cluster template.
func ensureClusterTemplate(ctx context.Context, admin, user *grpc.ClientConn, tenant string) error {
	// Reuse a tenant-owned pull Secret required by the cluster template.
	secrets := publicv1.NewSecretsClient(user)

	pullSecret, err := ensure(func() ([]*publicv1.Secret, error) {
		r, err := secrets.List(
			ctx,
			publicv1.SecretsListRequest_builder{Filter: catalogFilter("caas-it-pull-secret", tenant)}.Build(),
		)

		return r.GetItems(), err
	}, func() (*publicv1.Secret, error) {
		r, err := secrets.Create(ctx, publicv1.SecretsCreateRequest_builder{Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{Name: "caas-it-pull-secret", Tenant: tenant}.Build(),
			Type:     publicv1.SecretType_SECRET_TYPE_PULL_SECRET,
			// Discovery requires registry credentials; this reserved host is never contacted.
			Data: map[string][]byte{
				".dockerconfigjson": []byte(`{"auths":{"registry.caas-it.invalid":{"auth":"dGVzdDp0ZXN0"}}}`),
			},
		}.Build()}.Build())

		return r.GetObject(), err
	})
	if err != nil {
		return err
	}

	response, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: pullSecret.GetId()}.Build())
	if err != nil {
		return err
	}

	pullSecret = response.GetObject()
	if pullSecret.GetType() != publicv1.SecretType_SECRET_TYPE_PULL_SECRET {
		return fmt.Errorf("caas-it-pull-secret has an incompatible type")
	}

	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}

	if err := json.Unmarshal(pullSecret.GetData()[".dockerconfigjson"], &config); err != nil || len(config.Auths) == 0 {
		return fmt.Errorf("caas-it-pull-secret requires valid Docker configuration with nonempty auths")
	}

	sshKey, err := ensureClusterSSHKey(ctx, user, tenant)
	if err != nil {
		return err
	}

	// Create or reuse the production template with the expected release and Secret references.
	templates := privatev1.NewClusterTemplatesClient(admin)

	clusterTemplate, err := ensure(func() ([]*privatev1.ClusterTemplate, error) {
		r, err := templates.List(
			ctx,
			privatev1.ClusterTemplatesListRequest_builder{Filter: catalogFilter("caas-it-cluster", tenant)}.Build(),
		)

		return r.GetItems(), err
	}, func() (*privatev1.ClusterTemplate, error) {
		r, err := templates.Create(
			ctx,
			privatev1.ClusterTemplatesCreateRequest_builder{Object: privatev1.ClusterTemplate_builder{
				Id:          "osac.templates.ocp_ci_small",
				Metadata:    catalogMetadata("caas-it-cluster", tenant),
				Title:       "CI OpenShift Cluster",
				Description: "Production ocp_ci_small template",
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					Version:          privatev1.ClusterVersionReference_builder{Name: "caas-it-4-17-0", Shared: true}.Build(),
					PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "caas-it-pull-secret"}.Build(),
					SshKey:           privatev1.SecretReference_builder{Id: sshKey.GetId()}.Build(),
				}.Build(),
			}.Build()}.Build(),
		)

		return r.GetObject(), err
	})
	if err != nil {
		return err
	}

	// Verify reused template defaults and their tenant-owned Secret references.
	defaults := clusterTemplate.GetSpecDefaults()
	if clusterTemplate.GetId() != "osac.templates.ocp_ci_small" ||
		defaults.GetVersion().GetName() != "caas-it-4-17-0" ||
		!defaults.GetVersion().GetShared() ||
		defaults.GetPullSecretSecret().GetName() != "caas-it-pull-secret" ||
		defaults.GetSshKey().GetId() != sshKey.GetId() {
		return fmt.Errorf("caas-it-cluster selects incompatible template defaults")
	}

	return nil
}

// ensureClusterSSHKey registers a valid public key and verifies its stored material.
// Guest SSH access is outside this suite; only public fixture material is stored.
func ensureClusterSSHKey(ctx context.Context, user *grpc.ClientConn, tenant string) (*publicv1.Secret, error) {
	const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn caas-it\n"

	secrets := publicv1.NewSecretsClient(user)

	secret, err := ensure(func() ([]*publicv1.Secret, error) {
		r, err := secrets.List(
			ctx,
			publicv1.SecretsListRequest_builder{Filter: catalogFilter("caas-it-ssh-key", tenant)}.Build(),
		)

		return r.GetItems(), err
	}, func() (*publicv1.Secret, error) {
		r, err := secrets.Create(ctx, publicv1.SecretsCreateRequest_builder{Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{Name: "caas-it-ssh-key", Tenant: tenant}.Build(),
			Type:     publicv1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(publicKey)},
		}.Build()}.Build())

		return r.GetObject(), err
	})
	if err != nil {
		return nil, err
	}

	response, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: secret.GetId()}.Build())
	if err != nil {
		return nil, err
	}

	secret = response.GetObject()
	if secret.GetType() != publicv1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return nil, fmt.Errorf("caas-it-ssh-key has an incompatible type")
	}

	if _, _, _, _, err := ssh.ParseAuthorizedKey(secret.GetData()["public_key"]); err != nil {
		return nil, fmt.Errorf("caas-it-ssh-key requires a valid SSH public key: %w", err)
	}

	return secret, nil
}

// ensureGroupInstanceTypes keeps each inventory group's product host selector immutable.
func ensureGroupInstanceTypes(ctx context.Context, admin *grpc.ClientConn, groups []InventoryGroup) error {
	types := privatev1.NewBareMetalInstanceTypesClient(admin)

	for _, group := range groups {
		name := group.InstanceTypeName

		instanceType, err := ensure(func() ([]*privatev1.BareMetalInstanceType, error) {
			r, err := types.List(
				ctx,
				privatev1.BareMetalInstanceTypesListRequest_builder{Filter: catalogFilter(name, "shared")}.Build(),
			)

			return r.GetItems(), err
		}, func() (*privatev1.BareMetalInstanceType, error) {
			r, err := types.Create(
				ctx,
				privatev1.BareMetalInstanceTypesCreateRequest_builder{Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: "shared"}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Description: "Real allocation from simulated inventory",
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{"caas-it": "worker", harness.SlotLabel: group.Name},
						}.Build(),
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 4, Architecture: "amd64", ThreadsPerCore: 2}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 16}.Build(),
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "eth0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "10Gbps",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()}.Build(),
			)

			return r.GetObject(), err
		})
		if err != nil {
			return err
		}

		hardware := instanceType.GetSpec().GetHardware()
		if !maps.Equal(
			instanceType.GetSpec().GetHostLabelSelector().GetMatchLabels(),
			map[string]string{"caas-it": "worker", harness.SlotLabel: group.Name},
		) ||
			hardware.GetCpu().GetCores() != 4 ||
			hardware.GetCpu().GetArchitecture() != "amd64" ||
			hardware.GetMemory().GetTotalGb() != 16 {
			return fmt.Errorf("%s has incompatible host selector or hardware", name)
		}
	}

	return nil
}

// catalogFilter selects active prerequisites by name and tenant.
func catalogFilter(name, owner string) *string {
	return new(
		fmt.Sprintf(
			`this.metadata.name == %q && this.metadata.tenant == %q && !has(this.metadata.deletion_timestamp)`,
			name,
			owner,
		),
	)
}

// catalogMetadata identifies a private catalog prerequisite and its owning tenant.
func catalogMetadata(name, owner string) *privatev1.Metadata {
	return privatev1.Metadata_builder{Name: name, Tenant: owner}.Build()
}

// ensure reuses a unique matching prerequisite or creates one when none exists.
// Multiple matches are an error because choosing one would hide ambiguous setup.
func ensure[T any](list func() ([]T, error), create func() (T, error)) (T, error) {
	var zero T

	objects, err := list()
	if err != nil {
		return zero, err
	}

	switch len(objects) {
	case 0:
		slog.Debug("creating missing catalog prerequisite", "type", fmt.Sprintf("%T", zero))
		return create()
	case 1:
		slog.Debug("reusing unique catalog prerequisite", "type", fmt.Sprintf("%T", zero))
		return objects[0], nil
	default:
		return zero, fmt.Errorf("ambiguous test prerequisite: %d matches", len(objects))
	}
}
