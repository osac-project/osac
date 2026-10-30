/*
Copyright 2026.

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

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const (
	remoteKindClusterName            = "osac-remote"
	remoteKubeconfigSecretName       = "osac-remote-kubeconfig"
	remoteKubeconfigMountDir         = "/etc/osac-remote"
	remoteKubeconfigMountPath        = "/etc/osac-remote/remote-cluster-kubeconfig"
	remoteServiceAccountName         = "osac-remote-access"
	remoteRestrictedSAName           = "osac-remote-no-storage"
	remoteTestTenantName             = "tenant-two-kind"
	remoteTestSecretName             = "vast-tenant-config-tenant-two-kind"
	localDecoyStorageClass           = "local-mgmt-only-gold-sc"
	remoteWorkloadStorageClass       = "remote-workload-gold-sc"
	remoteWorkloadSilverStorageClass = "remote-workload-silver-sc"

	cspFixtureResourceName  = "csp-storage-fixture"
	cspFixtureCAConfigMap   = "csp-storage-fixture-ca"
	cspFixtureTLSSecret     = "csp-storage-fixture-tls"
	cspFixtureAuthSecret    = "csp-storage-fixture-auth"
	cspFixtureServiceFQDN   = "csp-storage-fixture.osac.svc.cluster.local"
	cspFixtureIssuerURL     = "https://csp-storage-fixture.osac.svc.cluster.local:8443/realms/osac"
	cspFixtureGRPCAddress   = "csp-storage-fixture.osac.svc.cluster.local:8444"
	cspFixtureGRPCEndpoint  = "https://csp-storage-fixture.osac.svc.cluster.local:8444"
	cspFixtureAAPControlURL = "http://csp-storage-fixture.osac.svc.cluster.local:8080/api/controller"

	cspFixtureClientIdentifier      = "osac-fixture-client"
	cspFixtureClientCredentialValue = "osac-fixture-client-credential"
	cspFixtureAccessMarker          = "osac-fixture-bearer-access-marker"
	cspFixtureAAPAuthValue          = "osac-fixture-aap-auth-marker"

	cspTenantAlphaName         = "tenant-csp-alpha"
	cspTenantBravoName         = "tenant-csp-bravo"
	cspTenantUnsupportedName   = "tenant-csp-unsupported"
	cspClusterOrderUnsupported = "clusterorder-csp-unsupported"
	cspTenantForbiddenName     = "tenant-csp-forbidden"

	localCSPDecoyStorageClass          = "local-decoy-csp-gold-sc"
	localCSPForbiddenDecoyStorageClass = "local-forbidden-decoy-csp-gold-sc"
	remoteCSPUnconfiguredBronzeSC      = "remote-csp-unconfigured-bronze-sc"
	remoteCSPGoldSC                    = "remote-csp-gold-sc"
	remoteCSPSilverSC1                 = "remote-csp-silver-sc-1"
	remoteCSPSilverSC2                 = "remote-csp-silver-sc-2"
	remoteCSPSilverSCDup               = "remote-csp-silver-sc-dup"
	remoteCSPSilverSCV2                = "remote-csp-silver-sc-v2"
	remoteCSPForbiddenGoldSC           = "remote-forbidden-csp-gold-sc"
)

type cspFixtureTier struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BackendID string `json:"backendId"`
	Protocol  string `json:"protocol"`
}

type cspFixtureBackend struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Provider               string `json:"provider"`
	CspManaged             bool   `json:"cspManaged"`
	Endpoint               string `json:"endpoint"`
	Username               string `json:"username"`
	InlineCredentialMarker string `json:"inlineCredentialMarker"`
	PasswordSecretID       string `json:"passwordSecretId"`
}

type cspFixtureState struct {
	Tiers     []cspFixtureTier    `json:"tiers"`
	Backends  []cspFixtureBackend `json:"backends"`
	FailTiers bool                `json:"failTiers"`
}

type cspFixtureStats struct {
	OIDCTokenRequests   int32    `json:"oidcTokenRequests"`
	TiersListCalls      int32    `json:"tiersListCalls"`
	BackendsListCalls   int32    `json:"backendsListCalls"`
	BackendsGetCalls    int32    `json:"backendsGetCalls"`
	SecretsGetCalls     int32    `json:"secretsGetCalls"`
	SecretsRequestedIDs []string `json:"secretsRequestedIds"`
	AAPLaunchCalls      int32    `json:"aapLaunchCalls"`
	AAPRequests         []string `json:"aapRequests"`
}

var _ = Describe("Remote cluster StorageClass reconciliation across two Kind clusters", Ordered, Serial, func() {
	var (
		remoteKubeconfigPath   string
		restorePatchFilePath   string
		remoteClusterCreated   bool
		remoteSecretUID        string
		fixtureCAUID           string
		fixtureTLSUID          string
		fixtureAuthUID         string
		fixtureServiceUID      string
		fixturePodUID          string
		remoteModeReconfigured bool
		tenantUIDs             map[string]string
		clusterOrderUIDs       map[string]string
		secretUIDs             map[string]string
		scUIDs                 map[string]string
	)

	BeforeAll(func() {
		tenantUIDs = make(map[string]string)
		clusterOrderUIDs = make(map[string]string)
		secretUIDs = make(map[string]string)
		scUIDs = make(map[string]string)

		_, lookErr := exec.LookPath("kind")
		Expect(lookErr).NotTo(HaveOccurred(), "kind binary must be present on PATH")

		clustersOut, err := runKind("get", "clusters")
		Expect(err).NotTo(HaveOccurred())
		mgmtCluster := mgmtKindClusterName()
		Expect(mgmtCluster).NotTo(
			Equal(remoteKindClusterName),
			"KIND_CLUSTER_NAME must not equal the remote test cluster name %q", remoteKindClusterName,
		)
		Expect(containsLine(string(clustersOut), mgmtCluster)).To(
			BeTrue(), "management Kind cluster %q must exist in `kind get clusters`", mgmtCluster,
		)
		Expect(containsLine(string(clustersOut), remoteKindClusterName)).To(
			BeFalse(), "remote Kind cluster %q must not already exist before test setup", remoteKindClusterName,
		)
		existingSecretOut, err := runMgmtKubectl(
			"get", "secret", remoteKubeconfigSecretName,
			"-n", operatorNamespace, "--ignore-not-found", "-o", "name",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(existingSecretOut))).To(
			BeEmpty(),
			"remote kubeconfig Secret %q must not already exist before test setup", remoteKubeconfigSecretName,
		)

		By("snapshotting the management cluster operator Deployment template as an RFC 6902 JSON Patch")
		restorePatchFilePath = filepath.Join(GinkgoT().TempDir(), "local-mode-restore-patch.json")
		writeLocalModeRestorePatchFile(restorePatchFilePath)

		remoteKubeconfigPath = filepath.Join(GinkgoT().TempDir(), "osac-remote.kubeconfig")

		DeferCleanup(func() {
			By("cleaning up test resources and restoring the operator Deployment to single-cluster mode")
			restorePatchApplied := !remoteModeReconfigured
			defer func() {
				if !restorePatchApplied {
					Fail("skipping remote Secret, fixture, and Kind cluster deletion because operator Deployment restore patch failed")
					return
				}
				var cleanupInfraErrs []error
				if fixturePodUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "Pod", operatorNamespace, cspFixtureResourceName, fixturePodUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if fixtureServiceUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "Service", operatorNamespace, cspFixtureResourceName, fixtureServiceUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if fixtureAuthUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "Secret", operatorNamespace, cspFixtureAuthSecret, fixtureAuthUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if fixtureTLSUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "Secret", operatorNamespace, cspFixtureTLSSecret, fixtureTLSUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if fixtureCAUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "ConfigMap", operatorNamespace, cspFixtureCAConfigMap, fixtureCAUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if remoteSecretUID != "" {
					if err := deleteMgmtObjectByUID(
						"v1", "Secret", operatorNamespace, remoteKubeconfigSecretName, remoteSecretUID,
					); err != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, err)
					}
				}
				if remoteClusterCreated {
					if _, kindErr := runKind(
						"delete", "cluster", "--name", remoteKindClusterName,
						"--kubeconfig", remoteKubeconfigPath,
					); kindErr != nil {
						cleanupInfraErrs = append(cleanupInfraErrs, kindErr)
					}
				}
				Expect(cleanupInfraErrs).To(BeEmpty())
			}()

			var cleanupErrs []error
			for coName, uid := range clusterOrderUIDs {
				if uid == "" {
					continue
				}
				if finErr := removeMgmtResourceFinalizers("clusterorder", coName, uid); finErr != nil {
					cleanupErrs = append(cleanupErrs, finErr)
				}
				if delErr := deleteMgmtObjectByUID(
					"osac.openshift.io/v1alpha1", "ClusterOrder", operatorNamespace, coName, uid,
				); delErr != nil {
					cleanupErrs = append(cleanupErrs, delErr)
				}
				if finErr := removeMgmtResourceFinalizers("clusterorder", coName, uid); finErr != nil {
					cleanupErrs = append(cleanupErrs, finErr)
				}
				waitForMgmtObjectDeleted("clusterorder", coName, operatorNamespace)
			}
			for tName, uid := range tenantUIDs {
				if uid == "" {
					continue
				}
				if finErr := removeMgmtResourceFinalizers("tenant", tName, uid); finErr != nil {
					cleanupErrs = append(cleanupErrs, finErr)
				}
				if delErr := deleteMgmtObjectByUID(
					"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace, tName, uid,
				); delErr != nil {
					cleanupErrs = append(cleanupErrs, delErr)
				}
				if finErr := removeMgmtResourceFinalizers("tenant", tName, uid); finErr != nil {
					cleanupErrs = append(cleanupErrs, finErr)
				}
				waitForMgmtObjectDeleted("tenant", tName, operatorNamespace)
			}
			for secName, uid := range secretUIDs {
				if uid == "" {
					continue
				}
				if secErr := deleteMgmtObjectByUID(
					"v1", "Secret", operatorNamespace, secName, uid,
				); secErr != nil {
					cleanupErrs = append(cleanupErrs, secErr)
				}
			}
			for scName, uid := range scUIDs {
				if uid == "" {
					continue
				}
				if scErr := deleteMgmtObjectByUID(
					"storage.k8s.io/v1", "StorageClass", "", scName, uid,
				); scErr != nil {
					cleanupErrs = append(cleanupErrs, scErr)
				}
			}

			var restoreErr error
			if remoteModeReconfigured {
				restorePatchApplied, restoreErr = restoreOperatorDeploymentToLocalMode(restorePatchFilePath)
			}
			Expect(restoreErr).NotTo(HaveOccurred())
			Expect(cleanupErrs).To(BeEmpty())
		})

		By("generating ephemeral ECDSA P-256 CA and server TLS certificates for csp-storage-fixture")
		caCertPEM, serverCertPEM, serverKeyPEM := generateCSPFixtureTLSBundle()
		createCSPFixtureConfigAndSecrets(
			caCertPEM, serverCertPEM, serverKeyPEM,
			&fixtureCAUID, &fixtureTLSUID, &fixtureAuthUID,
		)

		By("deploying Service/csp-storage-fixture and Pod/csp-storage-fixture and streaming the compiled fixture binary")
		deployAndStageCSPFixtureServer(&fixtureServiceUID, &fixturePodUID)

		By("creating the second Kind workload cluster (" + remoteKindClusterName + ")")
		remoteClusterCreated = true
		_, err = runKindWithTimeout(
			3*time.Minute,
			"create", "cluster",
			"--name", remoteKindClusterName,
			"--kubeconfig", remoteKubeconfigPath,
			"--wait", "90s",
		)
		Expect(err).NotTo(HaveOccurred())

		By("installing fake workload CRDs on the remote Kind cluster")
		projectDir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred())
		fakeCRDs := []string{
			filepath.Join(projectDir, "config/crd/fakes/k8s.ovn.org_userdefinednetworks.yaml"),
			filepath.Join(projectDir, "config/crd/fakes/kubevirt.io_virtualmachines.yaml"),
			filepath.Join(projectDir, "config/crd/fakes/kubevirt.io_virtualmachineinstances.yaml"),
		}
		for _, crdPath := range fakeCRDs {
			_, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath, "apply", "--server-side", "--force-conflicts", "-f", crdPath,
			)
			Expect(err).NotTo(HaveOccurred())
		}

		By("configuring a least-privilege ServiceAccount on the remote Kind cluster with read-only StorageClass RBAC")
		_, err = runKubectlWithStdin(remoteKubeconfigPath, remoteRBACManifest(), "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())

		remoteSAIdentity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, remoteServiceAccountName)
		for _, verb := range []string{"get", "list", "watch"} {
			out, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+remoteSAIdentity,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(lastNonEmptyLine(string(out))).To(
				Equal("yes"), "expected remote SA to allow %s on storageclasses", verb,
			)
		}
		for _, verb := range []string{"create", "update", "patch", "delete"} {
			out, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+remoteSAIdentity,
			)
			Expect(err).To(HaveOccurred(), "expected kubectl auth can-i %s to exit non-zero", verb)
			Expect(lastNonEmptyLine(string(out))).To(
				Equal("no"), "expected remote SA to forbid %s on storageclasses", verb,
			)
		}

		By("staging the remote cluster kubeconfig Secret on the management Kind cluster")
		inClusterKubeconfigFile := filepath.Join(GinkgoT().TempDir(), "in-cluster-remote.kubeconfig")
		writeRemoteInClusterKubeconfig(remoteKubeconfigPath, inClusterKubeconfigFile, remoteServiceAccountName)

		secretUIDOut, err := runMgmtKubectl(
			"create", "secret", "generic", remoteKubeconfigSecretName,
			"-n", operatorNamespace,
			"--from-file=kubeconfig="+inClusterKubeconfigFile,
			"-o", "jsonpath={.metadata.uid}",
		)
		Expect(err).NotTo(HaveOccurred())
		remoteSecretUID = strings.TrimSpace(string(secretUIDOut))
		Expect(remoteSecretUID).NotTo(BeEmpty())

		By("reconfiguring the deployed operator on the management cluster into remote-cluster + csp-storage-fixture mode")
		remoteModeReconfigured = true
		configureOperatorDeploymentForRemoteMode()
		waitForSingleReadyOperatorPod(remoteKubeconfigMountPath)
	})

	It("ignores management StorageClasses and reconciles remote StorageClasses via live watch", func() {
		By("ensuring csp-storage-fixture catalog is empty for the OSAC-managed remote StorageClass baseline")
		setCSPFixtureState(cspFixtureState{})
		Expect(getCSPFixtureStats().OIDCTokenRequests).To(BeNumerically(">=", 1))
		resetCSPFixtureStats()

		By("creating a decoy StorageClass on the local management cluster that must be ignored in remote mode")
		scUID, err := createMgmtFixtureFromYAML(storageClassYAML(localDecoyStorageClass, remoteTestTenantName, "gold"))
		Expect(err).NotTo(HaveOccurred())
		scUIDs[localDecoyStorageClass] = scUID

		By("creating the tenant storage hub Secret and Tenant CR on the management cluster")
		secUID, err := createMgmtFixtureFromYAML(
			tenantStorageSecretYAML(remoteTestSecretName, operatorNamespace, remoteTestTenantName),
		)
		Expect(err).NotTo(HaveOccurred())
		secretUIDs[remoteTestSecretName] = secUID

		tenantUID, err := createMgmtFixtureFromYAML(tenantCRYAML(remoteTestTenantName, operatorNamespace))
		Expect(err).NotTo(HaveOccurred())
		tenantUIDs[remoteTestTenantName] = tenantUID

		By("verifying StorageReconciler ignores the local decoy StorageClass and reports ClusterStorageReady=False")
		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)
			g.Expect(status.Finalizers).To(ContainElement("osac.openshift.io/storage"))
			g.Expect(status.Phase).To(Equal("Ready"))

			backendCond := findCondition(status.Conditions, "StorageBackendReady")
			g.Expect(backendCond).NotTo(BeNil())
			g.Expect(backendCond.Status).To(Equal("True"))

			clusterCond := findCondition(status.Conditions, "ClusterStorageReady")
			g.Expect(clusterCond).NotTo(BeNil())
			g.Expect(clusterCond.Status).To(Equal("False"))
			g.Expect(clusterCond.Reason).To(Equal("NotFound"))
			g.Expect(status.StorageClasses).To(BeEmpty())
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("creating a matching StorageClass on the remote Kind cluster without touching the management Tenant CR")
		_, err = runKubectlWithStdin(
			remoteKubeconfigPath,
			storageClassYAML(remoteWorkloadStorageClass, remoteTestTenantName, "gold"),
			"apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		By("verifying the remote StorageClass watch triggers reconciliation and populates management Tenant.status")
		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)

			clusterCond := findCondition(status.Conditions, "ClusterStorageReady")
			g.Expect(clusterCond).NotTo(BeNil())
			g.Expect(clusterCond.Status).To(Equal("True"))
			g.Expect(clusterCond.Reason).To(Equal("Found"))
			g.Expect(clusterCond.Message).To(ContainSubstring(remoteWorkloadStorageClass))
			g.Expect(clusterCond.Message).NotTo(ContainSubstring(localDecoyStorageClass))

			g.Expect(status.ClusterStorage).To(ContainElement(tenantClusterStorageEntry{
				ClusterName: "remote",
				Ready:       true,
				Reason:      "Found",
			}))
			g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
				{Name: remoteWorkloadStorageClass, Tier: "gold"},
			}))
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("creating a second-tier StorageClass on the remote Kind cluster and verifying live watch updates")
		_, err = runKubectlWithStdin(
			remoteKubeconfigPath,
			storageClassYAML(remoteWorkloadSilverStorageClass, remoteTestTenantName, "silver"),
			"apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)
			g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
				{Name: remoteWorkloadStorageClass, Tier: "gold"},
				{Name: remoteWorkloadSilverStorageClass, Tier: "silver"},
			}))
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("cleaning up Spec 1 OSAC-managed Tenant, Hub Secret, and StorageClasses without triggering AAP deprovisioning")
		Expect(deleteMgmtObjectByUID(
			"v1", "Secret", operatorNamespace, remoteTestSecretName, secretUIDs[remoteTestSecretName],
		)).To(Succeed())
		delete(secretUIDs, remoteTestSecretName)

		Expect(removeMgmtResourceFinalizers("tenant", remoteTestTenantName, tenantUIDs[remoteTestTenantName])).To(Succeed())
		Expect(deleteMgmtObjectByUID(
			"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace, remoteTestTenantName, tenantUIDs[remoteTestTenantName],
		)).To(Succeed())
		Expect(removeMgmtResourceFinalizers("tenant", remoteTestTenantName, tenantUIDs[remoteTestTenantName])).To(Succeed())
		waitForMgmtObjectDeleted("tenant", remoteTestTenantName, operatorNamespace)
		waitForMgmtObjectDeleted("secret", remoteTestSecretName, operatorNamespace)
		delete(tenantUIDs, remoteTestTenantName)

		Expect(deleteMgmtObjectByUID(
			"storage.k8s.io/v1", "StorageClass", "", localDecoyStorageClass, scUIDs[localDecoyStorageClass],
		)).To(Succeed())
		delete(scUIDs, localDecoyStorageClass)

		for _, scName := range []string{remoteWorkloadStorageClass, remoteWorkloadSilverStorageClass} {
			_, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath, "delete", "storageclass", scName, "--ignore-not-found",
			)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It(
		"reconciles CSP-managed StorageClass readiness, unconfigured tiers, live watch fan-out, "+
			"latching, and non-mutating Tenant deletion without Hub Secrets or AAP calls",
		func() {
			By("configuring csp-storage-fixture with two CSP-managed tiers (csp-gold, csp-silver) backed by netapp")
			activeCSPState := cspFixtureState{
				Tiers: []cspFixtureTier{
					{ID: "tier-csp-gold", Name: "csp-gold", BackendID: "backend-csp-netapp", Protocol: "block"},
					{ID: "tier-csp-silver", Name: "csp-silver", BackendID: "backend-csp-netapp", Protocol: "nfs"},
				},
				Backends: []cspFixtureBackend{
					{
						ID:               "backend-csp-netapp",
						Name:             "backend-csp-netapp",
						Provider:         "netapp",
						CspManaged:       true,
						PasswordSecretID: "csp-secret-never-fetched",
					},
				},
			}
			setCSPFixtureState(activeCSPState)
			resetCSPFixtureStats()

			By("creating a local management decoy csp-managed StorageClass and an unconfigured remote tier StorageClass")
			localDecoyUID, err := createMgmtFixtureFromYAML(
				storageClassYAMLWithProvisioner(localCSPDecoyStorageClass, "csp-managed", "csp-gold", "csi.example.com"),
			)
			Expect(err).NotTo(HaveOccurred())
			scUIDs[localCSPDecoyStorageClass] = localDecoyUID

			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(
					remoteCSPUnconfiguredBronzeSC, "csp-managed", "unconfigured-bronze", "csi.example.com",
				),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_, _ = runKubectlWithKubeconfig(
					remoteKubeconfigPath, "delete", "storageclass",
					remoteCSPUnconfiguredBronzeSC, remoteCSPGoldSC,
					remoteCSPSilverSC1, remoteCSPSilverSC2,
					remoteCSPSilverSCDup, remoteCSPSilverSCV2,
					"--ignore-not-found",
				)
			})

			By("creating two Tenant CRs (tenant-csp-alpha and tenant-csp-bravo) on the management cluster")
			alphaUID, err := createMgmtFixtureFromYAML(tenantCRYAML(cspTenantAlphaName, operatorNamespace))
			Expect(err).NotTo(HaveOccurred())
			tenantUIDs[cspTenantAlphaName] = alphaUID

			bravoUID, err := createMgmtFixtureFromYAML(tenantCRYAML(cspTenantBravoName, operatorNamespace))
			Expect(err).NotTo(HaveOccurred())
			tenantUIDs[cspTenantBravoName] = bravoUID

			By("verifying both Tenants report StorageReady=False while ignoring local decoy and unconfigured tier")
			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					g.Expect(status.Finalizers).To(ContainElement("osac.openshift.io/storage"))
					g.Expect(status.Phase).To(Equal("Ready"))

					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("False"))
					g.Expect(storageCond.Reason).To(Equal("NotFound"))
					g.Expect(status.StorageClasses).To(BeEmpty())
					g.Expect(findCondition(status.Conditions, "StorageBackendReady")).To(BeNil())
					g.Expect(findCondition(status.Conditions, "ClusterStorageReady")).To(BeNil())
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("creating remote-csp-gold-sc on osac-remote and verifying partial resolution (NotFound)")
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPGoldSC, "csp-managed", "csp-gold", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("False"))
					g.Expect(storageCond.Reason).To(Equal("NotFound"))
					g.Expect(storageCond.Message).To(ContainSubstring("csp-silver"))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("creating duplicate StorageClasses for csp-silver and verifying StorageReady=False (MultipleFound)")
			_, err = runKubectlWithKubeconfig(
				remoteKubeconfigPath, "delete", "storageclass", remoteCSPGoldSC,
			)
			Expect(err).NotTo(HaveOccurred())

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("False"))
					g.Expect(storageCond.Reason).To(Equal("NotFound"))
					g.Expect(status.StorageClasses).To(BeEmpty())
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPSilverSC1, "csp-managed", "csp-silver", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPSilverSC2, "csp-managed", "csp-silver", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPGoldSC, "csp-managed", "csp-gold", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("False"))
					g.Expect(storageCond.Reason).To(Equal("MultipleFound"))
					g.Expect(storageCond.Message).To(ContainSubstring("csp-silver"))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("deleting the duplicate remote-csp-silver-sc-2 so every configured CSP tier has a unique matching StorageClass")
			_, err = runKubectlWithKubeconfig(
				remoteKubeconfigPath, "delete", "storageclass", remoteCSPSilverSC2,
			)
			Expect(err).NotTo(HaveOccurred())

			latchedTransitionTimes := make(map[string]string, 2)
			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("True"))
					g.Expect(storageCond.Reason).To(Equal("Found"))
					g.Expect(storageCond.LastTransitionTime).NotTo(BeEmpty())
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
						{Name: remoteCSPSilverSC1, Tier: "csp-silver"},
					}))
					g.Expect(findCondition(status.Conditions, "StorageBackendReady")).To(BeNil())
					g.Expect(findCondition(status.Conditions, "ClusterStorageReady")).To(BeNil())
					latchedTransitionTimes[tName] = storageCond.LastTransitionTime
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("introducing a post-latching duplicate StorageClass and verifying StorageReady=True latches")
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPSilverSCDup, "csp-managed", "csp-silver", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("True"))
					g.Expect(storageCond.Reason).To(Equal("Found"))
					g.Expect(storageCond.LastTransitionTime).To(Equal(latchedTransitionTimes[tName]))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("replacing csp-silver StorageClasses with remote-csp-silver-sc-v2 and verifying live watch fan-out")
			_, err = runKubectlWithKubeconfig(
				remoteKubeconfigPath, "delete", "storageclass", remoteCSPSilverSC1, remoteCSPSilverSCDup,
			)
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPSilverSCV2, "csp-managed", "csp-silver", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("True"))
					g.Expect(storageCond.Reason).To(Equal("Found"))
					g.Expect(storageCond.LastTransitionTime).To(Equal(latchedTransitionTimes[tName]))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
						{Name: remoteCSPSilverSCV2, Tier: "csp-silver"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("toggling FailTiers=true and verifying StorageCatalogSyncRunnable preserves latched StorageReady=True")
			outageState := activeCSPState
			outageState.FailTiers = true
			setCSPFixtureState(outageState)

			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					backendCond := findCondition(status.Conditions, "StorageBackendReady")
					g.Expect(backendCond).NotTo(BeNil())
					g.Expect(backendCond.Status).To(Equal("False"))
					g.Expect(backendCond.Reason).To(Equal("TierResolutionFailed"))

					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("True"))
					g.Expect(storageCond.LastTransitionTime).To(Equal(latchedTransitionTimes[tName]))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
						{Name: remoteCSPSilverSCV2, Tier: "csp-silver"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("restoring FailTiers=false on csp-storage-fixture and verifying automatic recovery")
			setCSPFixtureState(activeCSPState)
			for _, tName := range []string{cspTenantAlphaName, cspTenantBravoName} {
				Eventually(func(g Gomega) {
					status := fetchTenantStatus(g, tName)
					g.Expect(findCondition(status.Conditions, "StorageBackendReady")).To(BeNil())
					storageCond := findCondition(status.Conditions, "StorageReady")
					g.Expect(storageCond).NotTo(BeNil())
					g.Expect(storageCond.Status).To(Equal("True"))
					g.Expect(storageCond.Reason).To(Equal("Found"))
					g.Expect(storageCond.LastTransitionTime).To(Equal(latchedTransitionTimes[tName]))
					g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
						{Name: remoteCSPGoldSC, Tier: "csp-gold"},
						{Name: remoteCSPSilverSCV2, Tier: "csp-silver"},
					}))
				}, 60*time.Second, 2*time.Second).Should(Succeed())
			}

			By("snapshotting UID and ResourceVersion of remote CSP StorageClasses before deleting Tenants")
			goldBefore := fetchRemoteStorageClassMeta(remoteKubeconfigPath, remoteCSPGoldSC)
			silverBefore := fetchRemoteStorageClassMeta(remoteKubeconfigPath, remoteCSPSilverSCV2)

			By("deleting CSP Tenants and verifying finalizer removal without mutating StorageClasses or calling AAP")
			for _, tName := range []string{cspTenantBravoName, cspTenantAlphaName} {
				Expect(deleteMgmtObjectByUID(
					"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace, tName, tenantUIDs[tName],
				)).To(Succeed())
				waitForMgmtObjectDeleted("tenant", tName, operatorNamespace)
				delete(tenantUIDs, tName)
			}

			stats := getCSPFixtureStats()
			Expect(stats.TiersListCalls).To(BeNumerically(">=", 1))
			Expect(stats.BackendsListCalls).To(BeNumerically(">=", 1))
			Expect(stats.SecretsGetCalls).To(
				BeZero(),
				"CSP-managed reconcile/delete must never call Secrets.Get (requested: %v)",
				stats.SecretsRequestedIDs,
			)
			Expect(stats.AAPLaunchCalls).To(
				BeZero(),
				"CSP-managed reconcile/delete must never call AAP (requests: %v)",
				stats.AAPRequests,
			)

			tenantLabeledSecretsOut, err := runMgmtKubectl(
				"get", "secrets", "-n", operatorNamespace,
				"-l", "osac.openshift.io/tenant", "-o", "name",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(tenantLabeledSecretsOut))).To(
				BeEmpty(), "zero Hub Secrets with osac.openshift.io/tenant should exist during CSP-managed lifecycle",
			)

			goldAfter := fetchRemoteStorageClassMeta(remoteKubeconfigPath, remoteCSPGoldSC)
			silverAfter := fetchRemoteStorageClassMeta(remoteKubeconfigPath, remoteCSPSilverSCV2)
			Expect(goldAfter.UID).To(Equal(goldBefore.UID))
			Expect(goldAfter.ResourceVersion).To(Equal(goldBefore.ResourceVersion))
			Expect(goldAfter.DeletionTimestamp).To(BeNil())
			Expect(silverAfter.UID).To(Equal(silverBefore.UID))
			Expect(silverAfter.ResourceVersion).To(Equal(silverBefore.ResourceVersion))
			Expect(silverAfter.DeletionTimestamp).To(BeNil())

			By("cleaning up Spec 2 StorageClasses")
			Expect(deleteMgmtObjectByUID(
				"storage.k8s.io/v1", "StorageClass", "", localCSPDecoyStorageClass, scUIDs[localCSPDecoyStorageClass],
			)).To(Succeed())
			delete(scUIDs, localCSPDecoyStorageClass)

			_, err = runKubectlWithKubeconfig(
				remoteKubeconfigPath, "delete", "storageclass",
				remoteCSPGoldSC, remoteCSPSilverSCV2, remoteCSPUnconfiguredBronzeSC,
				"--ignore-not-found",
			)
			Expect(err).NotTo(HaveOccurred())
		},
	)

	It(
		"fails closed for unsupported CSP-managed providers and CaaS ClusterOrders "+
			"without resolving credentials or dispatching AAP jobs",
		func() {
			By("configuring csp-storage-fixture with an unsupported CSP-managed provider")
			setCSPFixtureState(cspFixtureState{
				Tiers: []cspFixtureTier{
					{
						ID:        "tier-csp-unsupported",
						Name:      "csp-unsupported-tier",
						BackendID: "backend-csp-unsupported",
						Protocol:  "block",
					},
				},
				Backends: []cspFixtureBackend{
					{
						ID:                     "backend-csp-unsupported",
						Name:                   "backend-csp-unsupported",
						Provider:               "unsupported-csp-provider",
						CspManaged:             true,
						Endpoint:               "https://unsupported.csi.example.com",
						Username:               "unused-csp-user",
						InlineCredentialMarker: "inline-unused-marker",
						PasswordSecretID:       "unsupported-secret-never-fetched",
					},
				},
			})
			resetCSPFixtureStats()

			By("creating Tenant tenant-csp-unsupported and a Ready ClusterOrder on the management cluster")
			tUID, err := createMgmtFixtureFromYAML(tenantCRYAML(cspTenantUnsupportedName, operatorNamespace))
			Expect(err).NotTo(HaveOccurred())
			tenantUIDs[cspTenantUnsupportedName] = tUID

			coUID, err := createMgmtFixtureFromYAML(
				clusterOrderCRYAML(cspClusterOrderUnsupported, operatorNamespace, cspTenantUnsupportedName),
			)
			Expect(err).NotTo(HaveOccurred())
			clusterOrderUIDs[cspClusterOrderUnsupported] = coUID

			_, err = runMgmtKubectl(
				"patch", "clusterorder", cspClusterOrderUnsupported,
				"-n", operatorNamespace, "--subresource=status", "--type=merge",
				"-p", `{"status":{"phase":"Ready"}}`,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying tenant-csp-unsupported fails closed with StorageReady=False and no CaaS AAP job")
			Eventually(func(g Gomega) {
				status := fetchTenantStatus(g, cspTenantUnsupportedName)
				g.Expect(status.Phase).To(Equal("Ready"))

				storageCond := findCondition(status.Conditions, "StorageReady")
				g.Expect(storageCond).NotTo(BeNil())
				g.Expect(storageCond.Status).To(Equal("False"))
				g.Expect(storageCond.Reason).To(Equal("NotFound"))
				g.Expect(status.StorageClasses).To(BeEmpty())
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			Consistently(func(g Gomega) {
				coOut, coErr := runMgmtKubectl(
					"get", "clusterorder", cspClusterOrderUnsupported, "-n", operatorNamespace, "-o", "json",
				)
				g.Expect(coErr).NotTo(HaveOccurred())
				var co struct {
					Metadata struct {
						Finalizers []string `json:"finalizers"`
					} `json:"metadata"`
					Status struct {
						ClusterStorageJobs []any `json:"clusterStorageJobs"`
					} `json:"status"`
				}
				g.Expect(json.Unmarshal(coOut, &co)).To(Succeed())
				g.Expect(co.Metadata.Finalizers).NotTo(ContainElement("osac.openshift.io/cluster-storage"))
				g.Expect(co.Status.ClusterStorageJobs).To(BeEmpty())

				stats := getCSPFixtureStats()
				g.Expect(stats.SecretsGetCalls).To(BeZero())
				g.Expect(stats.AAPLaunchCalls).To(BeZero())
			}, 6*time.Second, time.Second).Should(Succeed())

			By("cleaning up ClusterOrder and tenant-csp-unsupported")
			Expect(removeMgmtResourceFinalizers(
				"clusterorder", cspClusterOrderUnsupported, clusterOrderUIDs[cspClusterOrderUnsupported],
			)).To(Succeed())
			Expect(deleteMgmtObjectByUID(
				"osac.openshift.io/v1alpha1", "ClusterOrder", operatorNamespace,
				cspClusterOrderUnsupported, clusterOrderUIDs[cspClusterOrderUnsupported],
			)).To(Succeed())
			waitForMgmtObjectDeleted("clusterorder", cspClusterOrderUnsupported, operatorNamespace)
			delete(clusterOrderUIDs, cspClusterOrderUnsupported)

			Expect(deleteMgmtObjectByUID(
				"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace,
				cspTenantUnsupportedName, tenantUIDs[cspTenantUnsupportedName],
			)).To(Succeed())
			waitForMgmtObjectDeleted("tenant", cspTenantUnsupportedName, operatorNamespace)
			delete(tenantUIDs, cspTenantUnsupportedName)

			stats := getCSPFixtureStats()
			Expect(stats.SecretsGetCalls).To(BeZero())
			Expect(stats.AAPLaunchCalls).To(BeZero())
		},
	)

	It(
		"rotates the remote kubeconfig Secret via watchRemoteKubeconfig, "+
			"fails closed on Forbidden RBAC without local fallback, and recovers",
		func() {
			By("provisioning a restricted ServiceAccount on the remote Kind cluster without StorageClass RBAC")
			_, err := runKubectlWithStdin(
				remoteKubeconfigPath, remoteRestrictedRBACManifest(false), "apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			restrictedIdentity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, remoteRestrictedSAName)
			nsOut, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", "list", "namespaces", "--as="+restrictedIdentity,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(lastNonEmptyLine(string(nsOut))).To(Equal("yes"))

			for _, verb := range []string{"get", "list", "watch"} {
				scOut, scErr := runKubectlWithKubeconfig(
					remoteKubeconfigPath,
					"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+restrictedIdentity,
				)
				Expect(scErr).To(HaveOccurred(), "expected restricted SA %s on storageclasses to exit non-zero", verb)
				Expect(lastNonEmptyLine(string(scOut))).To(Equal("no"))
			}

			By("rotating the remote kubeconfig Secret on the management cluster without modifying the Deployment")
			podName, initialRestarts := currentOperatorPodRestartCount()
			restrictedKubeconfigFile := filepath.Join(GinkgoT().TempDir(), "restricted-remote.kubeconfig")
			writeRemoteInClusterKubeconfig(remoteKubeconfigPath, restrictedKubeconfigFile, remoteRestrictedSAName)
			updateRemoteKubeconfigSecretAndTriggerSync(restrictedKubeconfigFile, podName)

			By("verifying watchRemoteKubeconfig detects the mounted Secret digest change and restarts the manager container")
			waitForOperatorContainerRestart(podName, initialRestarts)

			By("configuring csp-storage-fixture with csp-gold and creating StorageClasses + Tenant while RBAC is forbidden")
			setCSPFixtureState(cspFixtureState{
				Tiers: []cspFixtureTier{
					{ID: "tier-csp-gold", Name: "csp-gold", BackendID: "backend-csp-netapp", Protocol: "block"},
				},
				Backends: []cspFixtureBackend{
					{
						ID:               "backend-csp-netapp",
						Name:             "backend-csp-netapp",
						Provider:         "netapp",
						CspManaged:       true,
						PasswordSecretID: "csp-secret-never-fetched",
					},
				},
			})
			resetCSPFixtureStats()

			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAMLWithProvisioner(remoteCSPForbiddenGoldSC, "csp-managed", "csp-gold", "csi.example.com"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_, _ = runKubectlWithKubeconfig(
					remoteKubeconfigPath, "delete", "storageclass", remoteCSPForbiddenGoldSC, "--ignore-not-found",
				)
			})

			scUID, err := createMgmtFixtureFromYAML(
				storageClassYAMLWithProvisioner(localCSPForbiddenDecoyStorageClass, "csp-managed", "csp-gold", "csi.example.com"),
			)
			Expect(err).NotTo(HaveOccurred())
			scUIDs[localCSPForbiddenDecoyStorageClass] = scUID

			tenantUID, err := createMgmtFixtureFromYAML(tenantCRYAML(cspTenantForbiddenName, operatorNamespace))
			Expect(err).NotTo(HaveOccurred())
			tenantUIDs[cspTenantForbiddenName] = tenantUID

			By("verifying the operator logs the remote StorageClass Forbidden error and fails closed without local fallback")
			Eventually(func(g Gomega) {
				logsOut, logErr := runMgmtKubectl("logs", podName, "-n", operatorNamespace, "-c", "manager")
				g.Expect(logErr).NotTo(HaveOccurred())
				g.Expect(string(logsOut)).To(ContainSubstring("storageclasses.storage.k8s.io is forbidden"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			// multicluster-runtime's MultiClusterWatch waits for remoteCluster.GetCache().WaitForCacheSync(ctx)
			// before starting StorageReconciler workers, so a 403 Forbidden on remote StorageClasses blocks
			// cache sync and prevents StorageReconciler from adding its finalizer or falling back to local
			// StorageClasses until RBAC is granted.
			Consistently(func(g Gomega) {
				forbiddenStatus := fetchTenantStatus(g, cspTenantForbiddenName)
				g.Expect(forbiddenStatus.Finalizers).NotTo(ContainElement("osac.openshift.io/storage"))
				g.Expect(forbiddenStatus.StorageClasses).To(
					BeEmpty(), "StorageReconciler must fail closed and never fall back to local StorageClasses",
				)
				g.Expect(findCondition(forbiddenStatus.Conditions, "StorageReady")).To(BeNil())

				stats := getCSPFixtureStats()
				g.Expect(stats.SecretsGetCalls).To(BeZero())
				g.Expect(stats.AAPLaunchCalls).To(BeZero())
			}, 8*time.Second, time.Second).Should(Succeed())

			By("granting read-only StorageClass RBAC to the restricted ServiceAccount on the remote Kind cluster")
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath, remoteRestrictedRBACManifest(true), "apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator recovers automatically and resolves the remote CSP StorageClass to StorageReady=True")
			Eventually(func(g Gomega) {
				status := fetchTenantStatus(g, cspTenantForbiddenName)
				g.Expect(status.Finalizers).To(ContainElement("osac.openshift.io/storage"))
				g.Expect(status.Phase).To(Equal("Ready"))

				storageCond := findCondition(status.Conditions, "StorageReady")
				g.Expect(storageCond).NotTo(BeNil())
				g.Expect(storageCond.Status).To(Equal("True"))
				g.Expect(storageCond.Reason).To(Equal("Found"))
				g.Expect(storageCond.Message).To(ContainSubstring(remoteCSPForbiddenGoldSC))
				g.Expect(storageCond.Message).NotTo(ContainSubstring(localCSPForbiddenDecoyStorageClass))

				g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
					{Name: remoteCSPForbiddenGoldSC, Tier: "csp-gold"},
				}))
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			stats := getCSPFixtureStats()
			Expect(stats.SecretsGetCalls).To(BeZero())
			Expect(stats.AAPLaunchCalls).To(BeZero())

			By("cleaning up Spec 4 Tenant and local decoy StorageClass")
			Expect(removeMgmtResourceFinalizers(
				"tenant", cspTenantForbiddenName, tenantUIDs[cspTenantForbiddenName],
			)).To(Succeed())
			Expect(deleteMgmtObjectByUID(
				"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace,
				cspTenantForbiddenName, tenantUIDs[cspTenantForbiddenName],
			)).To(Succeed())
			Expect(removeMgmtResourceFinalizers(
				"tenant", cspTenantForbiddenName, tenantUIDs[cspTenantForbiddenName],
			)).To(Succeed())
			waitForMgmtObjectDeleted("tenant", cspTenantForbiddenName, operatorNamespace)
			delete(tenantUIDs, cspTenantForbiddenName)

			Expect(deleteMgmtObjectByUID(
				"storage.k8s.io/v1", "StorageClass", "",
				localCSPForbiddenDecoyStorageClass, scUIDs[localCSPForbiddenDecoyStorageClass],
			)).To(Succeed())
			delete(scUIDs, localCSPForbiddenDecoyStorageClass)
		},
	)
})

type tenantConditionEntry struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

type tenantClusterStorageEntry struct {
	ClusterName string `json:"clusterName"`
	Ready       bool   `json:"ready"`
	Reason      string `json:"reason"`
}

type tenantResolvedStorageClass struct {
	Name string `json:"name"`
	Tier string `json:"tier"`
}

type tenantStatusSnapshot struct {
	Finalizers     []string                     `json:"-"`
	Phase          string                       `json:"phase"`
	Conditions     []tenantConditionEntry       `json:"conditions"`
	ClusterStorage []tenantClusterStorageEntry  `json:"clusterStorage"`
	StorageClasses []tenantResolvedStorageClass `json:"storageClasses"`
}

type remoteStorageClassMeta struct {
	UID               string     `json:"uid"`
	ResourceVersion   string     `json:"resourceVersion"`
	DeletionTimestamp *time.Time `json:"deletionTimestamp"`
}

// fetchRemoteStorageClassMeta retrieves metadata (uid, resourceVersion, deletionTimestamp)
// of a StorageClass on the remote Kind cluster.
func fetchRemoteStorageClassMeta(remoteKubeconfigPath, scName string) remoteStorageClassMeta {
	out, err := runKubectlWithKubeconfig(remoteKubeconfigPath, "get", "storageclass", scName, "-o", "json")
	Expect(err).NotTo(HaveOccurred())
	var wrapper struct {
		Metadata remoteStorageClassMeta `json:"metadata"`
	}
	Expect(json.Unmarshal(out, &wrapper)).To(Succeed())
	Expect(wrapper.Metadata.UID).NotTo(BeEmpty())
	Expect(wrapper.Metadata.ResourceVersion).NotTo(BeEmpty())
	return wrapper.Metadata
}

// waitForMgmtObjectDeleted waits until the specified resource is NotFound on the management cluster.
func waitForMgmtObjectDeleted(resource, name, namespace string) {
	Eventually(func() bool {
		args := []string{"get", resource, name, "--ignore-not-found", "-o", "name"}
		if namespace != "" {
			args = append(args, "-n", namespace)
		}
		out, err := runMgmtKubectl(args...)
		return err == nil && strings.TrimSpace(string(out)) == ""
	}, 60*time.Second, time.Second).Should(BeTrue(), "expected %s/%s to be deleted", resource, name)
}

// generateCSPFixtureTLSBundle generates an ephemeral ECDSA P-256 CA and server
// certificate/key pair with SANs for Service/csp-storage-fixture in memory.
func generateCSPFixtureTLSBundle() (caCertPEM, serverCertPEM, serverKeyPEM []byte) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	now := time.Now().Add(-5 * time.Minute)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "osac-csp-fixture-ca"},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).NotTo(HaveOccurred())

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cspFixtureServiceFQDN},
		DNSNames: []string{
			cspFixtureServiceFQDN,
			cspFixtureResourceName + "." + operatorNamespace + ".svc",
			cspFixtureResourceName,
		},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	srvKeyDER, err := x509.MarshalECPrivateKey(srvKey)
	Expect(err).NotTo(HaveOccurred())

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	serverCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	serverKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvKeyDER})
	return caCertPEM, serverCertPEM, serverKeyPEM
}

// createCSPFixtureConfigAndSecrets creates ConfigMap/csp-storage-fixture-ca,
// Secret/csp-storage-fixture-tls, and Secret/csp-storage-fixture-auth in namespace osac,
// recording each UID immediately upon creation so DeferCleanup cleans up on partial failure.
func createCSPFixtureConfigAndSecrets(
	caCertPEM, serverCertPEM, serverKeyPEM []byte,
	caUIDOutPtr, tlsUIDOutPtr, authUIDOutPtr *string,
) {
	dir := GinkgoT().TempDir()
	caPath := filepath.Join(dir, "bundle.pem")
	crtPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	Expect(os.WriteFile(caPath, caCertPEM, 0o600)).To(Succeed())
	Expect(os.WriteFile(crtPath, serverCertPEM, 0o600)).To(Succeed())
	Expect(os.WriteFile(keyPath, serverKeyPEM, 0o600)).To(Succeed())

	caUIDOut, err := runMgmtKubectl(
		"create", "configmap", cspFixtureCAConfigMap,
		"-n", operatorNamespace,
		"--from-file=bundle.pem="+caPath,
		"-o", "jsonpath={.metadata.uid}",
	)
	Expect(err).NotTo(HaveOccurred())
	caUID := strings.TrimSpace(string(caUIDOut))
	Expect(caUID).NotTo(BeEmpty())
	*caUIDOutPtr = caUID

	tlsUIDOut, err := runMgmtKubectl(
		"create", "secret", "tls", cspFixtureTLSSecret,
		"-n", operatorNamespace,
		"--cert="+crtPath,
		"--key="+keyPath,
		"-o", "jsonpath={.metadata.uid}",
	)
	Expect(err).NotTo(HaveOccurred())
	tlsUID := strings.TrimSpace(string(tlsUIDOut))
	Expect(tlsUID).NotTo(BeEmpty())
	*tlsUIDOutPtr = tlsUID

	authUIDOut, err := runMgmtKubectl(
		"create", "secret", "generic", cspFixtureAuthSecret,
		"-n", operatorNamespace,
		"--from-literal=client-id="+cspFixtureClientIdentifier,
		"--from-literal=client-secret="+cspFixtureClientCredentialValue,
		"--from-literal=aap-token="+cspFixtureAAPAuthValue,
		"-o", "jsonpath={.metadata.uid}",
	)
	Expect(err).NotTo(HaveOccurred())
	authUID := strings.TrimSpace(string(authUIDOut))
	Expect(authUID).NotTo(BeEmpty())
	*authUIDOutPtr = authUID
}

// deployAndStageCSPFixtureServer cross-compiles test/integration/testdata/cspfixture
// for the Kind node architecture, deploys Service/csp-storage-fixture and Pod/csp-storage-fixture,
// streams the binary into /work/fixture-server, and waits for Pod condition=Ready.
func deployAndStageCSPFixtureServer(svcUIDOutPtr, podUIDOutPtr *string) {
	archOut, err := runMgmtKubectl("get", "nodes", "-o", "jsonpath={.items[0].status.nodeInfo.architecture}")
	Expect(err).NotTo(HaveOccurred())
	nodeArch := strings.TrimSpace(string(archOut))
	Expect(nodeArch).NotTo(BeEmpty())

	imgOut, err := runMgmtKubectl(
		"get", "deployments", "-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "jsonpath={.items[0].spec.template.spec.containers[0].image}",
	)
	Expect(err).NotTo(HaveOccurred())
	operatorImage := strings.TrimSpace(string(imgOut))
	Expect(operatorImage).NotTo(BeEmpty())

	projectDir, err := utils.GetProjectDir()
	Expect(err).NotTo(HaveOccurred())
	fixtureBinPath := filepath.Join(GinkgoT().TempDir(), "cspfixture-linux")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer buildCancel()
	buildCmd := exec.CommandContext(
		buildCtx, "go", "build", "-o", fixtureBinPath, "./test/integration/testdata/cspfixture",
	)
	buildCmd.Dir = projectDir
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+nodeArch)
	_, err = utils.Run(buildCmd)
	Expect(err).NotTo(HaveOccurred())

	svcUID, err := createMgmtFixtureFromYAML(cspFixtureServiceYAML())
	Expect(err).NotTo(HaveOccurred())
	*svcUIDOutPtr = svcUID

	podUID, err := createMgmtFixtureFromYAML(cspFixturePodYAML(operatorImage))
	Expect(err).NotTo(HaveOccurred())
	*podUIDOutPtr = podUID

	Eventually(func() string {
		phaseOut, phaseErr := runMgmtKubectl(
			"get", "pod", cspFixtureResourceName, "-n", operatorNamespace, "-o", "jsonpath={.status.phase}",
		)
		if phaseErr != nil {
			return ""
		}
		return strings.TrimSpace(string(phaseOut))
	}, 90*time.Second, time.Second).Should(Equal("Running"))

	binBytes, err := os.ReadFile(fixtureBinPath)
	Expect(err).NotTo(HaveOccurred())

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer streamCancel()
	stageScript := "cat > /work/fixture-server.tmp && " +
		"chmod 0755 /work/fixture-server.tmp && " +
		"mv /work/fixture-server.tmp /work/fixture-server"
	execCmd := exec.CommandContext(
		streamCtx,
		"kubectl", "--context="+mgmtKubeContext(),
		"exec", "-i", cspFixtureResourceName, "-n", operatorNamespace,
		"--", "/bin/sh", "-c", stageScript,
	)
	execCmd.Stdin = bytes.NewReader(binBytes)
	_, err = utils.Run(execCmd)
	Expect(err).NotTo(HaveOccurred())

	_, err = runMgmtKubectl(
		"wait", "--for=condition=Ready", "pod/"+cspFixtureResourceName,
		"-n", operatorNamespace, "--timeout=60s",
	)
	Expect(err).NotTo(HaveOccurred())
}

func cspFixtureServiceYAML() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app.kubernetes.io/name: %s
  ports:
    - name: grpc
      port: 8444
      targetPort: 8444
    - name: oidc
      port: 8443
      targetPort: 8443
    - name: http
      port: 8080
      targetPort: 8080
`, cspFixtureResourceName, operatorNamespace, cspFixtureResourceName)
}

func cspFixturePodYAML(image string) string {
	startScript := fmt.Sprintf(
		"while [ ! -x /work/fixture-server ]; do sleep 0.1; done; "+
			"exec /work/fixture-server --tls-cert=/tls/tls.crt --tls-key=/tls/tls.key "+
			"--oidc-addr=:8443 --grpc-addr=:8444 --http-addr=:8080 "+
			"--issuer-url=%s --client-id=%s --client-credential=%s --access-marker=%s",
		cspFixtureIssuerURL,
		cspFixtureClientIdentifier,
		cspFixtureClientCredentialValue,
		cspFixtureAccessMarker,
	)
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
spec:
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: fixture
      image: %q
      imagePullPolicy: IfNotPresent
      command:
        - /bin/sh
        - -c
        - %q
      ports:
        - containerPort: 8444
          name: grpc
        - containerPort: 8443
          name: oidc
        - containerPort: 8080
          name: http
      readinessProbe:
        httpGet:
          path: /healthz
          port: 8080
        initialDelaySeconds: 1
        periodSeconds: 1
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop:
            - ALL
      volumeMounts:
        - name: work
          mountPath: /work
        - name: tls
          mountPath: /tls
          readOnly: true
  volumes:
    - name: work
      emptyDir: {}
    - name: tls
      secret:
        secretName: %s
`,
		cspFixtureResourceName,
		operatorNamespace,
		cspFixtureResourceName,
		image,
		startScript,
		cspFixtureTLSSecret,
	)
}

func mgmtRESTConfig() *rest.Config {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: mgmtKubeContext()}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	Expect(err).NotTo(HaveOccurred())
	return restCfg
}

func callCSPFixtureAdminProxy(method, subPath string, body []byte) []byte {
	restCfg := mgmtRESTConfig()
	httpClient, err := rest.HTTPClientFor(restCfg)
	Expect(err).NotTo(HaveOccurred())

	proxyPath := fmt.Sprintf(
		"%s/api/v1/namespaces/%s/services/http:%s:8080/proxy/admin/%s",
		strings.TrimRight(restCfg.Host, "/"),
		operatorNamespace,
		cspFixtureResourceName,
		strings.TrimLeft(subPath, "/"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, proxyPath, reader)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(
		Equal(http.StatusOK),
		"unexpected status %d from %s /admin/%s: %s", resp.StatusCode, method, subPath, string(respBody),
	)
	return respBody
}

func getCSPFixtureStats() cspFixtureStats {
	raw := callCSPFixtureAdminProxy(http.MethodGet, "stats", nil)
	var stats cspFixtureStats
	Expect(json.Unmarshal(raw, &stats)).To(Succeed())
	return stats
}

func resetCSPFixtureStats() {
	_ = callCSPFixtureAdminProxy(http.MethodPost, "reset", []byte(`{}`))
}

// setCSPFixtureState updates the active catalog state on csp-storage-fixture and
// waits for StorageCatalogSyncRunnable to complete at least one full syncCatalog
// cycle after the update (`TiersListCalls >= before.TiersListCalls + 2`, plus
// `BackendsListCalls >= before.BackendsListCalls + 1` when !state.FailTiers).
func setCSPFixtureState(state cspFixtureState) {
	payload, err := json.Marshal(state)
	Expect(err).NotTo(HaveOccurred())
	_ = callCSPFixtureAdminProxy(http.MethodPost, "state", payload)
	before := getCSPFixtureStats()

	Eventually(func(g Gomega) {
		current := getCSPFixtureStats()
		g.Expect(current.TiersListCalls).To(BeNumerically(">=", before.TiersListCalls+2))
		if !state.FailTiers {
			g.Expect(current.BackendsListCalls).To(BeNumerically(">=", before.BackendsListCalls+1))
		}
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
}

// mgmtKindClusterName returns the management Kind cluster name from
// KIND_CLUSTER_NAME, defaulting to "osac-dev".
func mgmtKindClusterName() string {
	if name := strings.TrimSpace(os.Getenv("KIND_CLUSTER_NAME")); name != "" {
		return name
	}
	return "osac-dev"
}

// mgmtKubeContext returns the kubectl context name for the management Kind cluster.
func mgmtKubeContext() string {
	return "kind-" + mgmtKindClusterName()
}

// runMgmtKubectl executes kubectl against the management Kind cluster context.
func runMgmtKubectl(args ...string) ([]byte, error) {
	args = append([]string{"--context=" + mgmtKubeContext()}, args...)
	return runKubectl(args...)
}

// createMgmtFixtureFromYAML exclusively creates a fixture on the management
// cluster from YAML and returns its metadata.uid.
func createMgmtFixtureFromYAML(manifest string) (string, error) {
	out, err := runKubectlWithStdin("", manifest, "create", "-f", "-", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return "", err
	}
	uid := strings.TrimSpace(string(out))
	if uid == "" {
		return "", fmt.Errorf("created fixture returned empty metadata.uid")
	}
	return uid, nil
}

// deleteMgmtObjectByUID deletes an object on the management cluster with an
// atomic UID precondition, ignoring NotFound.
func deleteMgmtObjectByUID(apiVersion, kind, namespace, name, uid string) error {
	if uid == "" {
		return nil
	}
	restCfg := mgmtRESTConfig()
	c, err := client.New(restCfg, client.Options{})
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	uidTyped := types.UID(uid)
	ctx, cancel := context.WithTimeout(context.Background(), kubectlCommandTimeout)
	defer cancel()
	err = c.Delete(ctx, obj, client.Preconditions{UID: &uidTyped})
	return client.IgnoreNotFound(err)
}

// removeMgmtResourceFinalizers marks a resource unmanaged and clears its finalizers
// in the management cluster using an atomic RFC 6902 JSON Patch UID test, ignoring NotFound.
func removeMgmtResourceFinalizers(resource, name, uid string) error {
	if uid == "" {
		return nil
	}
	patch := fmt.Sprintf(
		`[{"op":"test","path":"/metadata/uid","value":%q},`+
			`{"op":"add","path":"/metadata/annotations","value":{"osac.openshift.io/management-state":"unmanaged"}},`+
			`{"op":"replace","path":"/metadata/finalizers","value":null}]`,
		uid,
	)
	out, err := runMgmtKubectl(
		"patch", resource, name, "-n", operatorNamespace,
		"--type=json", "-p", patch,
	)
	if err != nil && !strings.Contains(string(out), "NotFound") {
		return err
	}
	return nil
}

// fetchTenantStatus reads and unmarshals the Tenant status and metadata
// finalizers from the management cluster.
func fetchTenantStatus(g Gomega, name string) tenantStatusSnapshot {
	out, err := runMgmtKubectl("get", "tenant", name, "-n", operatorNamespace, "-o", "json")
	g.Expect(err).NotTo(HaveOccurred())

	var wrapper struct {
		Metadata struct {
			Finalizers []string `json:"finalizers"`
		} `json:"metadata"`
		Status tenantStatusSnapshot `json:"status"`
	}
	g.Expect(json.Unmarshal(out, &wrapper)).To(Succeed())
	wrapper.Status.Finalizers = wrapper.Metadata.Finalizers
	return wrapper.Status
}

// findCondition returns a pointer to the named condition in conds, or nil if absent.
func findCondition(conds []tenantConditionEntry, condType string) *tenantConditionEntry {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// runKind executes a kind CLI command with a 60s timeout.
func runKind(args ...string) ([]byte, error) {
	return runKindWithTimeout(60*time.Second, args...)
}

// runKindWithTimeout executes a kind CLI command with the given timeout and
// container runtime provider environment.
func runKindWithTimeout(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Env = os.Environ()
	if os.Getenv("KIND_EXPERIMENTAL_PROVIDER") == "" && os.Getenv("CONTAINER_TOOL") == "podman" {
		cmd.Env = append(cmd.Env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}
	return utils.Run(cmd)
}

// runKubectlWithKubeconfig runs kubectl against the specified kubeconfig path,
// or against the management Kind cluster context when kubeconfigPath is empty.
func runKubectlWithKubeconfig(kubeconfigPath string, args ...string) ([]byte, error) {
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		return runKubectl(args...)
	}
	return runMgmtKubectl(args...)
}

// runKubectlWithStdin runs kubectl with stdin populated from manifest, binding
// to the management Kind cluster context when kubeconfigPath is empty.
func runKubectlWithStdin(kubeconfigPath, manifest string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlCommandTimeout)
	defer cancel()
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
	} else {
		args = append([]string{"--context=" + mgmtKubeContext()}, args...)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	return utils.Run(cmd)
}

// containsLine reports whether output contains a line equal to target.
func containsLine(output, target string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// lastNonEmptyLine returns the last non-empty trimmed line of output so stderr
// warnings emitted by kubectl before stdout do not break single-token checks.
func lastNonEmptyLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// writeRemoteInClusterKubeconfig loads the remote Kind kubeconfig via clientcmd,
// points the cluster server at the control-plane node InternalIP on the shared
// Kind bridge network, sets the ServiceAccount bearer credential in AuthInfos,
// and writes the updated kubeconfig to destPath without logging credentials.
func writeRemoteInClusterKubeconfig(remoteKubeconfigPath, destPath, serviceAccountName string) {
	ipOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"get", "nodes", "-o", `jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`,
	)
	Expect(err).NotTo(HaveOccurred())
	nodeIP := strings.TrimSpace(string(ipOut))
	Expect(nodeIP).NotTo(BeEmpty())

	credOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"create", "token", serviceAccountName, "-n", operatorNamespace, "--duration=1h",
	)
	Expect(err).NotTo(HaveOccurred())
	bearerValue := strings.TrimSpace(string(credOut))
	Expect(bearerValue).NotTo(BeEmpty())

	kubeCfg, err := clientcmd.LoadFromFile(remoteKubeconfigPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(kubeCfg.Clusters).NotTo(BeEmpty())

	var clusterEntryName string
	for name, clusterCfg := range kubeCfg.Clusters {
		clusterEntryName = name
		clusterCfg.Server = "https://" + nodeIP + ":6443"
		break
	}
	Expect(clusterEntryName).NotTo(BeEmpty())

	kubeCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		serviceAccountName: {
			Token: bearerValue,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"remote": {
			Cluster:   clusterEntryName,
			AuthInfo:  serviceAccountName,
			Namespace: operatorNamespace,
		},
	}
	kubeCfg.CurrentContext = "remote"

	Expect(clientcmd.WriteToFile(*kubeCfg, destPath)).To(Succeed())
}

// updateRemoteKubeconfigSecretAndTriggerSync updates the remote kubeconfig Secret
// on the management cluster and annotates the running operator Pod so kubelet
// immediately syncs the projected Secret volume on disk.
func updateRemoteKubeconfigSecretAndTriggerSync(kubeconfigFile, podName string) {
	renderedSecret, err := runMgmtKubectl(
		"create", "secret", "generic", remoteKubeconfigSecretName,
		"-n", operatorNamespace,
		"--from-file=kubeconfig="+kubeconfigFile,
		"--dry-run=client", "-o", "yaml",
	)
	Expect(err).NotTo(HaveOccurred())

	_, err = runKubectlWithStdin("", string(renderedSecret), "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred())

	syncStamp := fmt.Sprintf("osac.openshift.io/kubeconfig-sync=%d", time.Now().UnixNano())
	_, err = runMgmtKubectl(
		"annotate", "pod", podName, "-n", operatorNamespace, syncStamp, "--overwrite",
	)
	Expect(err).NotTo(HaveOccurred())
}

// currentOperatorPodRestartCount returns the name and current manager container
// restartCount of the running operator controller-manager Pod.
func currentOperatorPodRestartCount() (string, int) {
	output, err := runMgmtKubectl(
		"get", "pods",
		"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "json",
	)
	Expect(err).NotTo(HaveOccurred())

	var podList struct {
		Items []struct {
			Metadata struct {
				Name              string     `json:"name"`
				DeletionTimestamp *time.Time `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					Name         string `json:"name"`
					RestartCount int    `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	Expect(json.Unmarshal(output, &podList)).To(Succeed())

	for _, pod := range podList.Items {
		if pod.Metadata.DeletionTimestamp != nil {
			continue
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "manager" {
				return pod.Metadata.Name, cs.RestartCount
			}
		}
	}
	Fail("running operator controller-manager pod not found")
	return "", 0
}

// waitForOperatorContainerRestart waits for watchRemoteKubeconfig to restart the
// manager container in podName and verifies the previous container log records
// the kubeconfig digest change.
func waitForOperatorContainerRestart(podName string, initialRestarts int) {
	Eventually(func(g Gomega) {
		output, err := runMgmtKubectl("get", "pod", podName, "-n", operatorNamespace, "-o", "json")
		g.Expect(err).NotTo(HaveOccurred())

		var pod struct {
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		}
		g.Expect(json.Unmarshal(output, &pod)).To(Succeed())
		g.Expect(pod.Status.Phase).To(Equal("Running"))

		var managerStatus *struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
		}
		for i := range pod.Status.ContainerStatuses {
			if pod.Status.ContainerStatuses[i].Name == "manager" {
				managerStatus = &pod.Status.ContainerStatuses[i]
				break
			}
		}
		g.Expect(managerStatus).NotTo(BeNil())
		g.Expect(managerStatus.RestartCount).To(BeNumerically(">", initialRestarts))
		g.Expect(managerStatus.Ready).To(BeTrue())

		prevLogs, logErr := runMgmtKubectl(
			"logs", podName, "-n", operatorNamespace, "-c", "manager", "--previous",
		)
		g.Expect(logErr).NotTo(HaveOccurred())
		g.Expect(string(prevLogs)).To(ContainSubstring("remote cluster kubeconfig changed; restarting operator"))
	}, 60*time.Second, 2*time.Second).Should(Succeed())
}

// configureOperatorDeploymentForRemoteMode patches the deployed operator on the
// management cluster to mount ConfigMap/csp-storage-fixture-ca on fulfillment-ca-bundle,
// mount Secret/osac-remote-kubeconfig at /etc/osac-remote/remote-cluster-kubeconfig,
// point Fulfillment and AAP at Service/csp-storage-fixture, and disable the
// incompatible Cluster and BareMetalInstance controllers.
func configureOperatorDeploymentForRemoteMode() {
	deployName := operatorDeploymentName()
	patch := fmt.Sprintf(`{
  "spec": {
    "template": {
      "spec": {
        "volumes": [
          {
            "name": "fulfillment-ca-bundle",
            "configMap": {
              "name": %q,
              "items": [
                {
                  "key": "bundle.pem",
                  "path": "bundle.pem"
                }
              ]
            }
          },
          {
            "name": "remote-kubeconfig",
            "secret": {
              "secretName": %q,
              "items": [
                {
                  "key": "kubeconfig",
                  "path": "remote-cluster-kubeconfig"
                }
              ]
            }
          }
        ],
        "containers": [
          {
            "name": "manager",
            "env": [
              {"name": "OSAC_REMOTE_CLUSTER_KUBECONFIG", "value": %q, "valueFrom": null},
              {"name": "OSAC_FULFILLMENT_SERVER_ADDRESS", "value": %q, "valueFrom": null},
              {"name": "OSAC_FULFILLMENT_ISSUER_URL", "value": %q, "valueFrom": null},
              {"name": "OSAC_FULFILLMENT_ENDPOINT", "value": %q, "valueFrom": null},
              {"name": "OSAC_AAP_URL", "value": %q, "valueFrom": null},
              {"name": "OSAC_AAP_STATUS_POLL_INTERVAL", "value": "1s", "valueFrom": null},
              {
                "name": "OSAC_FULFILLMENT_CLIENT_ID",
                "value": null,
                "valueFrom": {
                  "secretKeyRef": {
                    "name": %q,
                    "key": "client-id"
                  }
                }
              },
              {
                "name": "OSAC_FULFILLMENT_CLIENT_SECRET",
                "value": null,
                "valueFrom": {
                  "secretKeyRef": {
                    "name": %q,
                    "key": "client-secret"
                  }
                }
              },
              {
                "name": "OSAC_AAP_TOKEN",
                "value": null,
                "valueFrom": {
                  "secretKeyRef": {
                    "name": %q,
                    "key": "aap-token"
                  }
                }
              },
              {"name": "OSAC_ENABLE_CLUSTER_CONTROLLER", "value": "false", "valueFrom": null},
              {"name": "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER", "value": "false", "valueFrom": null},
              {"name": "OSAC_ENABLE_TENANT_CONTROLLER", "value": "true", "valueFrom": null},
              {"name": "OSAC_ENABLE_STORAGE_CONTROLLER", "value": "true", "valueFrom": null},
              {"name": "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER", "value": "true", "valueFrom": null},
              {"name": "OSAC_ENABLE_NETWORKING_CONTROLLER", "value": "true", "valueFrom": null},
              {"name": "OSAC_ENABLE_VOLUME_CONTROLLER", "value": "true", "valueFrom": null}
            ],
            "volumeMounts": [
              {
                "name": "fulfillment-ca-bundle",
                "mountPath": "/etc/ca-bundle",
                "readOnly": true
              },
              {
                "name": "remote-kubeconfig",
                "mountPath": %q,
                "readOnly": true
              }
            ]
          }
        ]
      }
    }
  }
}`,
		cspFixtureCAConfigMap,
		remoteKubeconfigSecretName,
		remoteKubeconfigMountPath,
		cspFixtureGRPCAddress,
		cspFixtureIssuerURL,
		cspFixtureGRPCEndpoint,
		cspFixtureAAPControlURL,
		cspFixtureAuthSecret,
		cspFixtureAuthSecret,
		cspFixtureAuthSecret,
		remoteKubeconfigMountDir,
	)

	patchFile := filepath.Join(GinkgoT().TempDir(), "remote-mode-patch.json")
	Expect(os.WriteFile(patchFile, []byte(patch), 0o600)).To(Succeed())

	_, err := runMgmtKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=strategic", "--patch-file="+patchFile,
	)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = utils.Run(exec.CommandContext(
		ctx, "kubectl", "--context="+mgmtKubeContext(),
		"rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	))
	Expect(err).NotTo(HaveOccurred())
}

// writeLocalModeRestorePatchFile snapshots the operator Deployment's original
// pod template spec as an RFC 6902 JSON Patch (`[{"op":"replace","path":"/spec/template","value":...}]`)
// so DeferCleanup atomically replaces /spec/template without strategic-merge leftovers.
func writeLocalModeRestorePatchFile(patchFilePath string) {
	savedListJSON, err := runMgmtKubectl(
		"get", "deployment", "-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "json",
	)
	Expect(err).NotTo(HaveOccurred())

	var list struct {
		Items []struct {
			Spec struct {
				Template json.RawMessage `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	Expect(json.Unmarshal(savedListJSON, &list)).To(Succeed())
	Expect(list.Items).NotTo(BeEmpty())

	patchOps := []map[string]any{
		{
			"op":    "replace",
			"path":  "/spec/template",
			"value": list.Items[0].Spec.Template,
		},
	}
	patchBody, err := json.Marshal(patchOps)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(patchFilePath, patchBody, 0o600)).To(Succeed())
}

// restoreOperatorDeploymentToLocalMode restores the operator Deployment's
// original pod template spec from restorePatchFilePath via RFC 6902 JSON Patch
// (--type=json) and verifies rollout.
func restoreOperatorDeploymentToLocalMode(restorePatchFilePath string) (bool, error) {
	deployName := operatorDeploymentName()
	if _, err := runMgmtKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=json", "--patch-file="+restorePatchFilePath,
	); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := utils.Run(exec.CommandContext(
		ctx, "kubectl", "--context="+mgmtKubeContext(),
		"rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	)); err != nil {
		return true, err
	}
	waitForSingleReadyOperatorPod("")
	return true, nil
}

// operatorDeploymentName returns the name of the deployed osac-operator Deployment.
func operatorDeploymentName() string {
	out, err := runMgmtKubectl(
		"get", "deployments", "-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "jsonpath={.items[0].metadata.name}",
	)
	Expect(err).NotTo(HaveOccurred())
	name := strings.TrimSpace(string(out))
	Expect(name).NotTo(BeEmpty())
	return name
}

// waitForSingleReadyOperatorPod waits until exactly one non-terminating operator
// controller-manager pod is Running, Ready, and carries wantRemoteKubeconfig in
// its OSAC_REMOTE_CLUSTER_KUBECONFIG environment variable.
func waitForSingleReadyOperatorPod(wantRemoteKubeconfig string) {
	Eventually(func() error {
		output, err := runMgmtKubectl(
			"get", "pods",
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-n", operatorNamespace, "-o", "json",
		)
		if err != nil {
			return err
		}
		var podList struct {
			Items []struct {
				Metadata struct {
					Name              string     `json:"name"`
					DeletionTimestamp *time.Time `json:"deletionTimestamp"`
				} `json:"metadata"`
				Spec struct {
					Containers []struct {
						Name string `json:"name"`
						Env  []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Ready bool `json:"ready"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(output, &podList); err != nil {
			return err
		}
		var readyCount int
		for _, pod := range podList.Items {
			if pod.Metadata.DeletionTimestamp != nil {
				continue
			}
			if pod.Status.Phase != "Running" {
				return fmt.Errorf("pod %s is in phase %s", pod.Metadata.Name, pod.Status.Phase)
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					return fmt.Errorf("pod %s container not ready", pod.Metadata.Name)
				}
			}
			var gotRemoteKubeconfig string
			for _, c := range pod.Spec.Containers {
				if c.Name != "manager" {
					continue
				}
				for _, env := range c.Env {
					if env.Name == "OSAC_REMOTE_CLUSTER_KUBECONFIG" {
						gotRemoteKubeconfig = env.Value
						break
					}
				}
			}
			if gotRemoteKubeconfig != wantRemoteKubeconfig {
				return fmt.Errorf(
					"pod %s has OSAC_REMOTE_CLUSTER_KUBECONFIG=%q, want %q",
					pod.Metadata.Name, gotRemoteKubeconfig, wantRemoteKubeconfig,
				)
			}
			readyCount++
		}
		if readyCount != 1 {
			return fmt.Errorf("expected 1 ready operator pod, found %d", readyCount)
		}
		return nil
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
}

// remoteRBACManifest returns the Namespace, ServiceAccount, ClusterRole, and
// ClusterRoleBinding manifest applied to the remote Kind cluster.
func remoteRBACManifest() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: osac-remote-storage-role
rules:
- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["k8s.ovn.org"]
  resources: ["userdefinednetworks"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["kubevirt.io"]
  resources: ["virtualmachines", "virtualmachineinstances"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: osac-remote-storage-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: osac-remote-storage-role
subjects:
- kind: ServiceAccount
  name: %s
  namespace: %s
`, operatorNamespace, remoteServiceAccountName, operatorNamespace, remoteServiceAccountName, operatorNamespace)
}

// remoteRestrictedRBACManifest returns the ServiceAccount, ClusterRole, and
// ClusterRoleBinding manifest for remoteRestrictedSAName, optionally including
// read-only StorageClass permissions when allowStorageClasses is true.
func remoteRestrictedRBACManifest(allowStorageClasses bool) string {
	storageClassRule := ""
	if allowStorageClasses {
		storageClassRule = `- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses"]
  verbs: ["get", "list", "watch"]
`
	}
	return fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: osac-remote-no-storage-role
rules:
%s- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["k8s.ovn.org"]
  resources: ["userdefinednetworks"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["kubevirt.io"]
  resources: ["virtualmachines", "virtualmachineinstances"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: osac-remote-no-storage-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: osac-remote-no-storage-role
subjects:
- kind: ServiceAccount
  name: %s
  namespace: %s
`, remoteRestrictedSAName, operatorNamespace, storageClassRule, remoteRestrictedSAName, operatorNamespace)
}

// storageClassYAML returns a StorageClass manifest labeled for tenantName and tierName.
func storageClassYAML(scName, tenantName, tierName string) string {
	return storageClassYAMLWithProvisioner(scName, tenantName, tierName, "kubernetes.io/no-provisioner")
}

// storageClassYAMLWithProvisioner returns a StorageClass manifest with the specified provisioner.
func storageClassYAMLWithProvisioner(scName, tenantName, tierName, provisioner string) string {
	return fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
  labels:
    osac.openshift.io/tenant: %s
    osac.openshift.io/storage-tier: %s
provisioner: %s
volumeBindingMode: WaitForFirstConsumer
`, scName, tenantName, tierName, provisioner)
}

// tenantStorageSecretYAML returns a tenant storage hub Secret manifest.
func tenantStorageSecretYAML(secretName, namespace, tenantName string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/tenant: %s
    osac.openshift.io/storage-provider: vast
type: Opaque
stringData:
  provider: vast
`, secretName, namespace, tenantName)
}

// tenantCRYAML returns a minimal Tenant CR manifest.
func tenantCRYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Tenant
metadata:
  name: %s
  namespace: %s
spec: {}
`, name, namespace)
}

// clusterOrderCRYAML returns a minimal ClusterOrder CR manifest annotated for tenantName.
func clusterOrderCRYAML(name, namespace, tenantName string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/tenant: %s
    osac.openshift.io/owner-reference: %s
spec:
  templateID: noop
`, name, namespace, tenantName, tenantName)
}
