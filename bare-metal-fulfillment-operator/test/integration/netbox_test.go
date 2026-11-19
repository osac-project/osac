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
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	bmfv1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/test/testhelpers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	netboxFixture            = "bmf-netbox-fixture"
	netboxBMFDeployment      = "bmf-operator-controller-manager"
	netboxTokenVolume        = "netbox-token"
	netboxTokenSecret        = "bmf-netbox-fixture-token"
	netboxRotatedTokenSecret = "bmf-netbox-fixture-rotated-token"
)

type netboxFixtureState struct {
	DeviceID      int
	Status        string
	Owner         string
	RotatedClaims int
}

var _ = Describe("NetBox inventory", Label("bmf-netbox"), func() {
	It("allocates and releases the host using the deployed Secret API and a rotated token", func(ctx SpecContext) {
		ns := os.Getenv("BMF_NETBOX_IT_NAMESPACE")
		if ns == "" {
			Skip("run the installer SUITE=bmf-netbox phase")
		}
		httpsPort := os.Getenv("BMF_NETBOX_IT_HTTPS_PORT")
		if httpsPort == "" {
			httpsPort = "8443"
		}

		kubeconfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
		).ClientConfig()
		Expect(err).NotTo(HaveOccurred())
		cluster, err := kubernetes.NewForConfig(kubeconfig)
		Expect(err).NotTo(HaveOccurred())
		deployment, err := cluster.AppsV1().Deployments(ns).Get(ctx, netboxBMFDeployment, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		volumes := deployment.Spec.Template.Spec.Volumes
		volumeIndex := slices.IndexFunc(volumes, func(volume corev1.Volume) bool {
			return volume.Name == netboxTokenVolume
		})
		Expect(volumeIndex).To(BeNumerically(">=", 0), "expected the chart to render the %s volume", netboxTokenVolume)
		volume := volumes[volumeIndex]
		Expect(volume.Secret).NotTo(BeNil(), "expected %s to reference a Secret", netboxTokenVolume)
		Expect(volume.Secret.SecretName).To(Equal(netboxTokenSecret), "expected the bmf.secrets.netboxToken value")

		fixture, err := getNetBoxFixtureState(ctx, cluster, ns, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(fixture.DeviceID).To(BeNumerically(">", 0))
		fixturePassword := rand.Text()
		seedNetBoxBMCSecret(ctx, cluster, ns, httpsPort, fixture.DeviceID, fixturePassword)

		By("allocating and releasing a host with the installed operator")
		runNetBoxAllocation(
			ctx, cluster, ns, fixture.DeviceID, fmt.Sprintf("netbox-it-%d", time.Now().UnixNano()), fixturePassword,
		)
		beforeRotation, err := getNetBoxFixtureState(ctx, cluster, ns, fixture.DeviceID)
		Expect(err).NotTo(HaveOccurred())

		By("changing the mounted token Secret and waiting for the Deployment to roll out")
		DeferCleanup(patchNetBoxTokenVolume, ns, volumeIndex, netboxTokenSecret)
		patchNetBoxTokenVolume(ctx, ns, volumeIndex, netboxRotatedTokenSecret)

		By("allocating and releasing again and verifying the operator used the rotated token")
		runNetBoxAllocation(
			ctx, cluster, ns, fixture.DeviceID, fmt.Sprintf("netbox-it-rotated-%d", time.Now().UnixNano()), fixturePassword,
		)
		afterRotation, err := getNetBoxFixtureState(ctx, cluster, ns, fixture.DeviceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(afterRotation.RotatedClaims).To(BeNumerically(">", beforeRotation.RotatedClaims),
			"expected a successful claim PATCH using the rotated token")
		for _, name := range []string{netboxTokenSecret, netboxRotatedTokenSecret, "fulfillment-controller-credentials"} {
			_, err := cluster.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "expected %s to remain after allocation and release", name)
		}
	})
})

func seedNetBoxBMCSecret(
	ctx context.Context, cluster kubernetes.Interface, ns, port string, deviceID int, fixturePassword string,
) {
	bundle, err := cluster.CoreV1().ConfigMaps(ns).Get(ctx, "ca-bundle", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	pool := x509.NewCertPool()
	Expect(pool.AppendCertsFromPEM([]byte(bundle.Data["bundle.pem"]))).To(BeTrue())

	// Use the installer's emergency administrator for fixture creation. The BMF
	// deployment retrieves the fixture with its own OAuth client.
	token, err := cluster.CoreV1().ServiceAccounts(ns).CreateToken(ctx, "admin",
		&authenticationv1.TokenRequest{}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	host := "fulfillment-internal-api." + ns + ".svc.cluster.local"
	conn, err := grpc.NewClient(
		"passthrough:///"+net.JoinHostPort("127.0.0.1", port),
		grpc.WithAuthority(host),
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool})),
		grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.Status.Token}),
		}),
	)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(conn.Close)
	client := privatev1.NewSecretsClient(conn)
	name := fmt.Sprintf("netbox-bmc-it-%d", time.Now().UnixNano())
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := client.Create(callCtx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name: name, Tenant: "system",
				Labels:      map[string]string{fmt.Sprintf("osac.openshift.io/netbox-device-%d", deviceID): "true"},
				Annotations: netboxFixtureAnnotations(name),
			}.Build(),
			Data: map[string][]byte{
				"username": []byte("fixture-user"),
				"password": []byte(fixturePassword),
			},
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		_, err := client.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: response.GetObject().GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
	}, NodeTimeout(30*time.Second))
}

func runNetBoxAllocation(
	ctx context.Context, cluster kubernetes.Interface, ns string, deviceID int, name, fixturePassword string,
) {
	netboxHost := fmt.Sprintf("netbox-%d", deviceID)
	netboxRuntimeSecret := netboxHost + "-bmc-secret"
	request, err := json.Marshal(bmfv1alpha1.BareMetalInstance{
		TypeMeta:   metav1.TypeMeta{APIVersion: bmfv1alpha1.GroupVersion.String(), Kind: "BareMetalInstance"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: netboxFixtureAnnotations(name)},
		Spec: bmfv1alpha1.BareMetalInstanceSpec{
			Selector:    bmfv1alpha1.HostSelectorSpec{HostSelector: map[string]string{"fixture": "kind"}},
			TemplateID:  "noop",
			RunStrategy: bmfv1alpha1.RunStrategyAlways,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	create := exec.CommandContext(ctx, "kubectl", "create", "-n", ns, "-f", "-")
	create.Stdin = bytes.NewReader(request)
	output, err := create.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	DeferCleanup(testhelpers.DeleteAndWaitForCleanup, ns, "BareMetalInstance", name)

	Eventually(ctx, func(g Gomega) {
		state, err := getNetBoxFixtureState(ctx, cluster, ns, deviceID)
		g.Expect(err).NotTo(HaveOccurred())
		instance, err := testhelpers.Get(ns, "BareMetalInstance", name)
		g.Expect(err).NotTo(HaveOccurred())
		host, _, err := unstructured.NestedString(instance.Object, "spec", "externalHostID")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(host).To(Equal(ns + "/" + netboxHost))
		g.Expect(state.Status).To(Equal("active"))
		g.Expect(state.Owner).To(Equal(string(instance.GetUID())))
		_, err = testhelpers.Get(ns, "BareMetalHost", netboxHost)
		g.Expect(err).NotTo(HaveOccurred())
		secret, err := cluster.CoreV1().Secrets(ns).Get(ctx, netboxRuntimeSecret, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		matchesFixture := string(secret.Data["username"]) == "fixture-user" &&
			subtle.ConstantTimeCompare(secret.Data["password"], []byte(fixturePassword)) == 1
		g.Expect(matchesFixture).To(BeTrue(), "expected the fixture BMC credentials")
	}, 2*time.Minute, 2*time.Second).Should(Succeed())

	output, err = exec.CommandContext(ctx, "kubectl", "delete", "baremetalinstance", name,
		"-n", ns, "--wait=false").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	Eventually(ctx, func(g Gomega) {
		state, err := getNetBoxFixtureState(ctx, cluster, ns, deviceID)
		g.Expect(err).NotTo(HaveOccurred())
		instanceAbsent, err := testhelpers.Absent(ns, "BareMetalInstance", name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(instanceAbsent).To(BeTrue())
		hostAbsent, err := testhelpers.Absent(ns, "BareMetalHost", netboxHost)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hostAbsent).To(BeTrue())
		_, err = cluster.CoreV1().Secrets(ns).Get(ctx, netboxRuntimeSecret, metav1.GetOptions{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the BMC Secret to be deleted: %v", err)
		g.Expect(state.Status).To(Equal("staged"))
		g.Expect(state.Owner).To(BeEmpty())
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
}

func getNetBoxFixtureState(
	ctx context.Context, cluster kubernetes.Interface, ns string, deviceID int,
) (netboxFixtureState, error) {
	var state netboxFixtureState
	data, err := cluster.CoreV1().Services(ns).ProxyGet("http", netboxFixture, "http", "fixture-state", nil).DoRaw(ctx)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if deviceID != 0 && state.DeviceID != deviceID {
		return state, fmt.Errorf("NetBox fixture restarted during the test: device ID changed from %d to %d",
			deviceID, state.DeviceID)
	}
	return state, nil
}

// The Helm chart renders bmf.secrets.netboxToken as the netbox-token volume.
func patchNetBoxTokenVolume(ctx SpecContext, ns string, volumeIndex int, secretName string) {
	volumePath := fmt.Sprintf("/spec/template/spec/volumes/%d", volumeIndex)
	patch := fmt.Sprintf(`[
		{"op":"test","path":%q,"value":%q},
		{"op":"replace","path":%q,"value":%q}
	]`, volumePath+"/name", netboxTokenVolume, volumePath+"/secret/secretName", secretName)
	deployment := "deployment/" + netboxBMFDeployment
	output, err := exec.CommandContext(ctx, "kubectl", "patch", deployment,
		"-n", ns, "--type=json", "-p", patch).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	output, err = exec.CommandContext(ctx, "kubectl", "rollout", "status", deployment,
		"-n", ns, "--timeout=3m").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
}

func netboxFixtureAnnotations(name string) map[string]string {
	const tenantAnnotation = "osac.openshift.io/tenant"
	const ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
	return map[string]string{tenantAnnotation: "system", ownerReferenceAnnotation: name}
}
