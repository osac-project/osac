/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const computeStorageRecorderName = "compute-storage-aap"

type computeStorageCapture struct {
	ID       int    `json:"id"`
	Template string `json:"template"`
	Status   string `json:"status"`
	Payload  struct {
		ExtraVars struct {
			JobVars struct {
				Resource        osacv1alpha1.ComputeInstance        `json:"resource"`
				Classes         []osacv1alpha1.ResolvedStorageClass `json:"tenant_storage_classes"`
				Connections     map[string]json.RawMessage          `json:"storage_backend_connections"`
				TierDefinitions []json.RawMessage                   `json:"storage_tier_definitions"`
			} `json:"osac_job_vars"`
		} `json:"extra_vars"`
	} `json:"payload"`
}

type computeStorageCaptures struct {
	Jobs       []computeStorageCapture `json:"jobs"`
	Unexpected []string                `json:"unexpected"`
}

type computeStorageFixture struct {
	tierIDs   []string
	tierNames []string
	classes   []*storagev1.StorageClass
	spec      *publicv1.ComputeInstanceSpec
}

func installComputeStorageRecorder(ctx context.Context) {
	GinkgoHelper()
	objects := []crclient.Object{
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: computeStorageRecorderName, Namespace: hubNamespace},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": computeStorageRecorderName},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: computeStorageRecorderName, Namespace: hubNamespace,
				Labels: map[string]string{"app": computeStorageRecorderName},
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: new(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: new(true), RunAsUser: new(int64(1000)),
					FSGroup:        new(int64(1000)),
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				Containers: []corev1.Container{{
					Name:  "recorder",
					Image: "registry.access.redhat.com/ubi10-minimal:1791444565",
					Command: []string{"/bin/sh", "-c",
						"while [ ! -x /work/recorder ]; do sleep 0.1; done; exec /work/recorder"},
					Ports:        []corev1.ContainerPort{{ContainerPort: 8080}},
					VolumeMounts: []corev1.VolumeMount{{Name: "fixture", MountPath: "/work"}},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: new(false), ReadOnlyRootFilesystem: new(true),
						Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(8080)},
					}},
				}},
				Volumes: []corev1.Volume{{Name: "fixture", VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}}},
			},
		},
	}
	for _, object := range objects {
		Expect(tool.KubeClient().Create(ctx, object)).To(Succeed())
		DeferCleanup(func(cleanupCtx context.Context) {
			err := tool.KubeClient().Delete(cleanupCtx, object)
			Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue())
		})
	}
	var nodeName string
	Eventually(func(g Gomega) {
		pod, err := tool.KubeClientSet().CoreV1().Pods(hubNamespace).Get(ctx, computeStorageRecorderName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		nodeName = pod.Spec.NodeName
	}, opTimeout, time.Second).Should(Succeed())
	node, err := tool.KubeClientSet().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(node.Status.NodeInfo.Architecture).NotTo(BeEmpty())
	buildCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	binaryPath := filepath.Join(GinkgoT().TempDir(), "recorder")
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binaryPath, "./it/testdata/computestorageaap")
	build.Dir = tool.projectDir
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+node.Status.NodeInfo.Architecture)
	output, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "build AAP recorder: %s", output)
	binary, err := os.ReadFile(binaryPath)
	Expect(err).NotTo(HaveOccurred())
	stageCtx, cancelStage := context.WithTimeout(ctx, time.Minute)
	defer cancelStage()
	stage := exec.CommandContext(stageCtx, "kubectl", "--kubeconfig", tool.kcFile,
		"exec", "-i", "-n", hubNamespace, computeStorageRecorderName, "-c", "recorder", "--", "/bin/sh", "-c",
		"cat > /work/recorder.tmp && chmod 0755 /work/recorder.tmp && mv /work/recorder.tmp /work/recorder")
	stage.Stdin = bytes.NewReader(binary)
	// Discard kubectl output: connection and credential-plugin errors may contain sensitive data.
	err = stage.Run()
	Expect(err).NotTo(HaveOccurred(), "stage AAP recorder")
	Eventually(func(g Gomega) {
		_, err := tool.KubeClientSet().CoreV1().Services(hubNamespace).
			ProxyGet("http", computeStorageRecorderName, "8080", "healthz", nil).DoRaw(ctx)
		g.Expect(err).NotTo(HaveOccurred())
	}, opTimeout, time.Second).Should(Succeed())
}

func computeStorageRecorderRequest(ctx context.Context, path string, payload any) []byte {
	GinkgoHelper()
	request := tool.KubeClientSet().CoreV1().RESTClient().Get()
	if payload != nil {
		body, err := json.Marshal(payload)
		Expect(err).NotTo(HaveOccurred())
		request = tool.KubeClientSet().CoreV1().RESTClient().Post().Body(body)
	}
	data, err := request.Namespace(hubNamespace).Resource("services").Name("http:" + computeStorageRecorderName + ":8080").
		SubResource("proxy").Suffix(path).Timeout(10 * time.Second).DoRaw(ctx)
	Expect(err).NotTo(HaveOccurred())
	return data
}

func readComputeStorageCaptures(ctx context.Context) computeStorageCaptures {
	GinkgoHelper()
	var captures computeStorageCaptures
	Expect(json.Unmarshal(computeStorageRecorderRequest(ctx, "captures", nil), &captures)).To(Succeed())
	return captures
}

func newComputeStorageFixture(ctx context.Context) *computeStorageFixture {
	GinkgoHelper()
	f := &computeStorageFixture{}
	backends := privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
	backend, err := backends.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
		Object: privatev1.StorageBackend_builder{
			Metadata: resourceFixtureMetadata("shared", ""),
			Spec:     privatev1.StorageBackendSpec_builder{Provider: "netapp", CspManaged: true}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	deferResourceFixtureDeletion(func(cleanupCtx context.Context) error {
		_, err := backends.Delete(cleanupCtx, privatev1.StorageBackendsDeleteRequest_builder{Id: backend.GetObject().GetId()}.Build())
		return err
	}, nil)
	tiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
	for range 2 {
		response, createErr := tiers.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: resourceFixtureMetadata("shared", ""),
				Spec: privatev1.StorageTierSpec_builder{
					Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{BackendId: backend.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(createErr).NotTo(HaveOccurred())
		tier := response.GetObject()
		f.tierIDs = append(f.tierIDs, tier.GetId())
		f.tierNames = append(f.tierNames, tier.GetMetadata().GetName())
		deferResourceFixtureDeletion(func(cleanupCtx context.Context) error {
			_, err := tiers.Delete(cleanupCtx, privatev1.StorageTiersDeleteRequest_builder{Id: tier.GetId()}.Build())
			return err
		}, nil)
	}
	network := createNetworkFixture(ctx, usersGroup, "")
	instanceType := createComputeInstanceTypeFixture(ctx)
	image := createDiskImageFixture(ctx, "shared", resourceFixtureName())
	template := createComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceType}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Id: image.GetId()}.Build(),
		BootDisk: privatev1.ComputeInstanceDisk_builder{
			SizeGib: new(int32(30)), StorageTier: privatev1.StorageTierReference_builder{Id: f.tierIDs[0]}.Build(),
		}.Build(),
		RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
	}.Build(), nil)
	f.spec = publicv1.ComputeInstanceSpec_builder{
		Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
		NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
		AdditionalDisks: publicv1.ComputeInstanceDiskList_builder{Items: []*publicv1.ComputeInstanceDisk{
			publicv1.ComputeInstanceDisk_builder{
				SizeGib: new(int32(12)), StorageTier: publicv1.StorageTierReference_builder{Id: f.tierIDs[1]}.Build(),
			}.Build(),
		}}.Build(),
	}.Build()
	Eventually(func(g Gomega) {
		tenant := &osacv1alpha1.Tenant{}
		g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Namespace: hubNamespace, Name: usersGroup}, tenant)).To(Succeed())
		g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := tool.KubeClient().Get(ctx, crclient.ObjectKeyFromObject(tenant), tenant); err != nil {
				return err
			}
			tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
			tenant.Status.Namespace = usersGroup
			return tool.KubeClient().Status().Update(ctx, tenant)
		})).To(Succeed())
	}, opTimeout, time.Second).Should(Succeed())
	DeferCleanup(func(cleanupCtx context.Context) {
		for _, sc := range f.classes {
			err := tool.KubeClient().Delete(cleanupCtx, sc)
			Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue())
		}
	})
	return f
}

func (f *computeStorageFixture) addClass(ctx context.Context, tier int) {
	GinkgoHelper()
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: resourceFixtureName(), Labels: map[string]string{
			"osac.openshift.io/tenant": "csp-managed", "osac.openshift.io/storage-tier": f.tierNames[tier],
		}},
		Provisioner: "csi.trident.netapp.io",
	}
	Expect(tool.KubeClient().Create(ctx, sc)).To(Succeed())
	f.classes = append(f.classes, sc)
}

func (f *computeStorageFixture) removeLastClass(ctx context.Context) {
	GinkgoHelper()
	Expect(tool.KubeClient().Delete(ctx, f.classes[len(f.classes)-1])).To(Succeed())
	f.classes = f.classes[:len(f.classes)-1]
}

func (f *computeStorageFixture) expectClassesUnchanged(ctx context.Context) {
	GinkgoHelper()
	for _, expected := range f.classes {
		current := &storagev1.StorageClass{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKeyFromObject(expected), current)).To(Succeed())
		Expect(current.UID).To(Equal(expected.UID))
		Expect(current.ResourceVersion).To(Equal(expected.ResourceVersion))
		Expect(current.Labels).To(Equal(expected.Labels))
		Expect(current.Provisioner).To(Equal(expected.Provisioner))
	}
}

func (f *computeStorageFixture) createInstance(ctx context.Context) *publicv1.ComputeInstance {
	GinkgoHelper()
	instances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
	response, err := instances.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
		Object: publicv1.ComputeInstance_builder{
			Metadata: publicv1.Metadata_builder{Name: resourceFixtureName()}.Build(), Spec: f.spec,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		_, deleteErr := instances.Delete(cleanupCtx, publicv1.ComputeInstancesDeleteRequest_builder{Id: id}.Build())
		Expect(deleteErr == nil || status.Code(deleteErr) == codes.NotFound).To(BeTrue())
		Eventually(func(g Gomega) {
			for _, job := range readComputeStorageCaptures(cleanupCtx).Jobs {
				if job.Status == "running" {
					computeStorageRecorderRequest(cleanupCtx, fmt.Sprintf("complete/%d", job.ID), map[string]any{"status": "successful"})
				}
			}
			_, getErr := instances.Get(cleanupCtx, publicv1.ComputeInstancesGetRequest_builder{Id: id}.Build())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, opTimeout, time.Second).Should(Succeed())
		f.expectClassesUnchanged(cleanupCtx)
		Expect(readComputeStorageCaptures(cleanupCtx).Unexpected).To(BeEmpty())
	})
	return response.GetObject()
}
