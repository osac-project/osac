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

package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftipnet "github.com/openshift/hypershift/api/util/ipnet"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/assistedservice"
)

var _ = Describe("ClusterOrder management publication", func() {
	It("ignores a TLS connection close error after a successful handshake", func() {
		certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		DeferCleanup(certificateServer.Close)
		rootCAs := x509.NewCertPool()
		rootCAs.AddCert(certificateServer.Certificate())

		serverConnection, clientConnection := net.Pipe()
		server := tls.Server(serverConnection, &tls.Config{
			Certificates: certificateServer.TLS.Certificates,
		})
		serverHandshake := make(chan error, 1)
		go func() {
			serverHandshake <- server.Handshake()
			_ = server.Close()
		}()

		client := tls.Client(closeErrorConn{
			Conn:     clientConnection,
			closeErr: fmt.Errorf("simulated close error"),
		}, &tls.Config{RootCAs: rootCAs, ServerName: "127.0.0.1"})
		Expect(handshakeAndClose(context.Background(), client)).To(Succeed())
		Expect(<-serverHandshake).To(Succeed())
	})

	It("propagates transient errors reading the HostedCluster kubeconfig Secret", func() {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		readErr := fmt.Errorf("simulated API timeout")
		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object,
					opts ...client.GetOption) error {
					if _, isSecret := obj.(*corev1.Secret); isSecret {
						return readErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		reconciler := &ClusterOrderReconciler{Client: k8sClient}
		hc := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "caas-management-test"},
			Status: hypershiftv1beta1.HostedClusterStatus{
				KubeConfig: &corev1.LocalObjectReference{Name: "hosted-cluster-kubeconfig"},
			},
		}

		roots, check, err := reconciler.checkHostedClusterKubeconfig(ctx, hc)
		Expect(roots).To(BeNil())
		Expect(check).To(Equal(managementPublicationCheck{}))
		Expect(err).To(MatchError("get HostedCluster kubeconfig Secret: simulated API timeout"))

		missingSecretClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		reconciler.Client = missingSecretClient
		_, check, err = reconciler.checkHostedClusterKubeconfig(ctx, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.ready).To(BeFalse())
		Expect(check.reason).To(Equal(managementPublicationPending))
	})

	It("requires the API VIP, public routes, and kubeconfig to pass their checks", func() {
		const (
			clusterNamespace = "caas-management-test"
			clusterName      = "cluster-a"
			hcpNamespace     = clusterNamespace + "-" + clusterName
			apiHost          = "api." + clusterName + ".example.test"
			apiVIP           = "192.0.2.10"
			hcpUID           = types.UID("hcp-uid")
			orderUID         = types.UID("order-uid")
		)

		_, podNetwork, err := net.ParseCIDR("10.128.0.0/14")
		Expect(err).NotTo(HaveOccurred())
		_, serviceNetwork, err := net.ParseCIDR("172.31.0.0/16")
		Expect(err).NotTo(HaveOccurred())

		serviceTypes := []hypershiftv1beta1.ServiceType{
			hypershiftv1beta1.OAuthServer,
			hypershiftv1beta1.Konnectivity,
			hypershiftv1beta1.Ignition,
		}
		routeHosts := map[hypershiftv1beta1.ServiceType]string{
			hypershiftv1beta1.OAuthServer:  "oauth.example.test",
			hypershiftv1beta1.Konnectivity: "konnectivity.example.test",
			hypershiftv1beta1.Ignition:     "ignition.example.test",
		}
		services := make([]hypershiftv1beta1.ServicePublishingStrategyMapping, 0, len(serviceTypes))
		objects := make([]runtime.Object, 0, len(serviceTypes)+4)
		for _, serviceType := range serviceTypes {
			host := routeHosts[serviceType]
			services = append(services, hypershiftv1beta1.ServicePublishingStrategyMapping{
				Service: serviceType,
				ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
					Type:  hypershiftv1beta1.Route,
					Route: &hypershiftv1beta1.RoutePublishingStrategy{Hostname: host},
				},
			})
		}
		objects = append(objects,
			managementRoute(oauthExternalPublicRouteName, routeHosts[hypershiftv1beta1.OAuthServer]),
			managementRoute(konnectivityServerRouteName, routeHosts[hypershiftv1beta1.Konnectivity]),
			managementRoute(ignitionServerRouteName, routeHosts[hypershiftv1beta1.Ignition]),
		)
		unrelatedRoute := managementRoute("metrics", "metrics.example.test")
		delete(unrelatedRoute.Object["spec"].(map[string]interface{}), "tls")
		delete(unrelatedRoute.Object, "status")
		objects = append(objects, unrelatedRoute)

		hcpRootCA, hcpRootCAPEM := managementTestCA("hcp-root-ca")
		konnectivityCA, konnectivityCAPEM := managementTestCA("konnectivity-serving-ca")
		ignitionCA, ignitionCAPEM := managementTestCA("ignition-serving-ca")
		ingressCA, _ := managementTestCA("management-ingress-ca")
		objects = append(objects,
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: hcpRootCASecretName, Namespace: hcpNamespace},
				Data:       map[string][]byte{"ca.crt": hcpRootCAPEM},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: hcpIgnitionServerCASecretName, Namespace: hcpNamespace},
				Data:       map[string][]byte{corev1.TLSCertKey: ignitionCAPEM},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: hcpKonnectivityServingCAName, Namespace: hcpNamespace},
				Data:       map[string][]byte{"ca.crt": konnectivityCAPEM},
			},
		)

		hc := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: clusterNamespace},
			Spec: hypershiftv1beta1.HostedClusterSpec{
				DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
				Networking: hypershiftv1beta1.ClusterNetworking{
					ClusterNetwork: []hypershiftv1beta1.ClusterNetworkEntry{{CIDR: hypershiftipnet.IPNet(*podNetwork)}},
					ServiceNetwork: []hypershiftv1beta1.ServiceNetworkEntry{{CIDR: hypershiftipnet.IPNet(*serviceNetwork)}},
				},
				Services: services,
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				ControlPlaneEndpoint: hypershiftv1beta1.APIEndpoint{Host: apiHost, Port: 6443},
				KubeConfig:           &corev1.LocalObjectReference{Name: "hosted-cluster-kubeconfig"},
				Conditions: []metav1.Condition{
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: "Ready"},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, Reason: "Ready"},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, Reason: "Ready"},
				},
			},
		}
		hcp := &hypershiftv1beta1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: hcpNamespace, UID: hcpUID},
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kube-apiserver", Namespace: hcpNamespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: hypershiftv1beta1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       hcp.Name,
					UID:        hcpUID,
				}},
			},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{{Name: "https", Port: 6443}},
			},
			Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{IP: apiVIP}},
			}},
		}
		caServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		DeferCleanup(caServer.Close)
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caServer.Certificate().Raw})
		kubeconfigData, err := managementKubeconfig(apiHost, caPEM)
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "hosted-cluster-kubeconfig", Namespace: clusterNamespace},
			Data:       map[string][]byte{"kubeconfig": kubeconfigData},
		}
		infraEnv := assistedservice.NewInfraEnvObject()
		infraEnv.SetName(assistedservice.InfraEnvName("order-a"))
		infraEnv.SetNamespace(clusterNamespace)
		controllerRef := true
		infraEnv.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: v1alpha1.GroupVersion.String(), Kind: "ClusterOrder", Name: "order-a",
			UID: orderUID, Controller: &controllerRef,
		}})
		Expect(unstructured.SetNestedField(infraEnv.Object, "https://assisted.example.test:6443/ignition",
			"status", "bootArtifacts", "discoveryIgnitionURL")).To(Succeed())
		objects = append(objects, hc, hcp, service, secret, infraEnv)

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
		probes := make([]string, 0, len(managementRouteIdentities)+2)
		var apiRootCAsPresent bool
		var oauthRouteRootCAsPresent bool
		var konnectivityRouteRootCAsPresent bool
		var ignitionRouteRootCAsPresent bool
		var ignitionRootCAsPresent bool
		ingressRoots := x509.NewCertPool()
		ingressRoots.AddCert(ingressCA)
		reconciler := &ClusterOrderReconciler{
			Client:             k8sClient,
			ManagementRootCAs:  ingressRoots,
			StatusPollInterval: time.Second,
			managementProbe: func(_ context.Context, serverName string, port int, connectIP string, rootCAs *x509.CertPool) error {
				probes = append(probes, fmt.Sprintf("%s:%d:%s", serverName, port, connectIP))
				if connectIP != "" {
					apiRootCAsPresent = rootCAs != nil
				} else if serverName == routeHosts[hypershiftv1beta1.OAuthServer] {
					oauthRouteRootCAsPresent = managementRootPoolIncludes(rootCAs, hcpRootCA) &&
						!managementRootPoolIncludes(rootCAs, ingressCA)
				} else if serverName == routeHosts[hypershiftv1beta1.Konnectivity] {
					konnectivityRouteRootCAsPresent = managementRootPoolIncludes(rootCAs, konnectivityCA) &&
						!managementRootPoolIncludes(rootCAs, ingressCA)
				} else if serverName == routeHosts[hypershiftv1beta1.Ignition] {
					ignitionRouteRootCAsPresent = managementRootPoolIncludes(rootCAs, ignitionCA) &&
						!managementRootPoolIncludes(rootCAs, hcpRootCA) &&
						!managementRootPoolIncludes(rootCAs, ingressCA)
				} else if serverName == "assisted.example.test" {
					ignitionRootCAsPresent = rootCAs == ingressRoots
				}
				return nil
			},
		}
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "order-a", Namespace: clusterNamespace, UID: orderUID,
		}, Spec: v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{{
			NodeSet: "workers", NumberOfNodes: 1,
			BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "worker"},
		}}}}

		ready, err := reconciler.reconcileManagementPublication(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(instance.Status.ApiEndpoint).To(Equal(apiVIP))
		Expect(probes).To(HaveLen(len(managementRouteIdentities) + 2))
		Expect(probes).To(ContainElement(apiHost + ":6443:" + apiVIP))
		Expect(apiRootCAsPresent).To(BeTrue())
		Expect(oauthRouteRootCAsPresent).To(BeTrue())
		Expect(konnectivityRouteRootCAsPresent).To(BeTrue())
		Expect(ignitionRouteRootCAsPresent).To(BeTrue())
		Expect(ignitionRootCAsPresent).To(BeTrue())
		Expect(probes).To(ContainElement("assisted.example.test:6443:"))
		for _, serviceType := range []hypershiftv1beta1.ServiceType{
			hypershiftv1beta1.OAuthServer,
			hypershiftv1beta1.Konnectivity,
			hypershiftv1beta1.Ignition,
		} {
			Expect(probes).To(ContainElement(routeHosts[serviceType] + ":443:"))
		}
		Expect(probes).NotTo(ContainElement("oidc.example.test:443:"))
		Expect(probes).NotTo(ContainElement("metrics.example.test:443:"))

		condition := apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))

		disabledInstance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "order-disabled", Namespace: clusterNamespace,
		}}
		Expect(reconciler.reconcileClusterEndpointLifecycle(ctx, disabledInstance, hc, true)).To(Succeed())
		managementCondition := apimeta.FindStatusCondition(disabledInstance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
		Expect(managementCondition).NotTo(BeNil())
		Expect(managementCondition.Status).To(Equal(metav1.ConditionTrue))
		endpointCondition := apimeta.FindStatusCondition(disabledInstance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionEndpointsReady))
		Expect(endpointCondition).NotTo(BeNil())
		Expect(endpointCondition.Status).To(Equal(metav1.ConditionTrue))
		Expect(endpointCondition.Reason).To(Equal(endpointReasonProvisioningDisabled))
		Expect(disabledInstance.Status.ApiEndpoint).To(Equal(apiVIP))

		phaseTransitionTime := metav1.NewTime(time.Now().Add(-time.Hour))
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseReady
		instance.Status.PhaseTransitionTime = &phaseTransitionTime
		instance.Status.APIURL = "https://api.cluster-a.example.test:6443"
		instance.Status.ConsoleURL = "https://console-openshift-console.apps.cluster-a.example.test"
		desiredWorkers, currentWorkers, readyWorkers := int32(1), int32(1), int32(1)
		instance.Status.DesiredWorkers = &desiredWorkers
		instance.Status.CurrentWorkers = &currentWorkers
		instance.Status.ReadyWorkers = &readyWorkers
		instance.Status.Jobs = []v1alpha1.NetworkJobStatus{
			{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateSucceeded},
			{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateSucceeded},
		}
		setClusterEndpointCondition(instance, metav1.ConditionTrue,
			endpointReasonEndpointsPublished, "API and ingress endpoints are attached and published")
		instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionFalse,
			"", v1alpha1.ReasonAsExpected)

		networkHarness := newNetHarness(true)
		reconciler.Engine = networkHarness.engine
		managementProbe := reconciler.managementProbe
		failFirstProbe := true
		reconciler.managementProbe = func(
			ctx context.Context, serverName string, port int, connectIP string, rootCAs *x509.CertPool,
		) error {
			if failFirstProbe {
				failFirstProbe = false
				return fmt.Errorf("simulated transient TLS timeout")
			}
			return managementProbe(ctx, serverName, port, connectIP, rootCAs)
		}

		Expect(reconciler.reconcileClusterEndpointLifecycle(ctx, instance, hc, true)).To(Succeed())
		managementCondition = apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
		Expect(managementCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(managementCondition.Reason).To(Equal(managementPublicationProbeFailed))
		Expect(hostedClusterControlPlaneIsAvailable(hc)).To(BeTrue())
		Expect(hostedClusterIsReady(hc)).To(BeTrue())
		Expect(clusterEndpointDNSPublicationEstablished(instance)).To(BeTrue())
		probeGrace := reconciler.endpointProbeFailureGracePeriod()
		managementFailureTime := managementCondition.LastTransitionTime.Time
		Expect(clusterOrderHasTransientEndpointProbeFailure(instance,
			v1alpha1.ClusterOrderPhaseReady, managementFailureTime.Add(time.Second), probeGrace)).To(BeTrue())
		Expect(clusterOrderHasTransientEndpointProbeFailure(instance,
			v1alpha1.ClusterOrderPhaseReady,
			managementFailureTime.Add(probeGrace+time.Nanosecond), probeGrace)).To(BeFalse(),
			"a management probe failure must stop preserving Ready after the grace period")
		expiredManagementFailure := instance.DeepCopy()
		reconciler.now = func() time.Time {
			return managementFailureTime.Add(probeGrace + time.Nanosecond)
		}
		reconciler.reconcileClusterOrderReadiness(expiredManagementFailure, hc, true,
			v1alpha1.ClusterOrderPhaseReady)
		Expect(expiredManagementFailure.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
			"the readiness gate must regress an order after the management probe grace expires")
		reconciler.now = nil
		Expect(bareMetalWorkersReady(instance)).To(BeTrue())
		newlyReadyOrder := instance.DeepCopy()
		reconciler.reconcileClusterOrderReadiness(newlyReadyOrder, hc, true,
			v1alpha1.ClusterOrderPhaseProgressing)
		Expect(newlyReadyOrder.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
			"an order that first becomes Ready in this reconcile must still pass the endpoint gate")

		reconciler.reconcileClusterOrderReadiness(instance, hc, true,
			v1alpha1.ClusterOrderPhaseReady)
		Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
		Expect(instance.Status.PhaseTransitionTime.Time).To(Equal(phaseTransitionTime.Time))

		ready, err = reconciler.reconcileManagementPublication(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue(), "the one-off probe failure recovers on the next check")
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonGuestIngressProgress, endpointMessageWaitingForGuestAPI)
		guestIngressCondition := apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionEndpointsReady))
		guestIngressFailureTime := guestIngressCondition.LastTransitionTime.Time
		Expect(clusterOrderHasTransientEndpointProbeFailure(instance,
			v1alpha1.ClusterOrderPhaseReady, guestIngressFailureTime.Add(time.Second), probeGrace)).To(BeTrue())
		Expect(clusterOrderHasTransientEndpointProbeFailure(instance,
			v1alpha1.ClusterOrderPhaseReady,
			guestIngressFailureTime.Add(probeGrace+time.Nanosecond), probeGrace)).To(BeFalse(),
			"a guest ingress failure must stop preserving Ready after the grace period")
		expiredGuestIngressFailure := instance.DeepCopy()
		reconciler.now = func() time.Time {
			return guestIngressFailureTime.Add(probeGrace + time.Nanosecond)
		}
		reconciler.reconcileClusterOrderReadiness(expiredGuestIngressFailure, hc, true,
			v1alpha1.ClusterOrderPhaseReady)
		Expect(expiredGuestIngressFailure.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
			"the readiness gate must regress an order after the guest ingress grace expires")
		reconciler.now = nil
		reconciler.reconcileClusterOrderReadiness(instance, hc, true,
			v1alpha1.ClusterOrderPhaseReady)
		Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady),
			"a transient guest API transport failure does not regress published endpoints")

		instance.Status.ApiEndpoint = "192.0.2.11"
		ready, err = reconciler.reconcileManagementPublication(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(instance.Status.ApiEndpoint).To(Equal("192.0.2.11"))
		condition = apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
		Expect(condition.Reason).To(Equal(managementPublicationVIPChanged))
		reconciler.reconcileClusterOrderReadiness(instance, hc, true,
			v1alpha1.ClusterOrderPhaseReady)
		Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
			"a changed management API VIP still regresses endpoint readiness")
	})

	It("uses the configured management roots for edge and reencrypt Routes", func() {
		roots := x509.NewCertPool()
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-a", Namespace: "caas-management-test",
		}}
		reconciler := &ClusterOrderReconciler{ManagementRootCAs: roots}
		for _, termination := range []routev1.TLSTerminationType{
			routev1.TLSTerminationEdge,
			routev1.TLSTerminationReencrypt,
		} {
			selected, check, err := reconciler.managementRouteRootCAs(context.Background(), hc,
				managementRouteEndpoint{service: hypershiftv1beta1.OAuthServer, termination: termination})
			Expect(err).NotTo(HaveOccurred())
			Expect(check.ready).To(BeTrue())
			Expect(selected).To(BeIdenticalTo(roots))
		}
	})

	It("waits for the HostedControlPlane serving CA before probing a passthrough Route", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-a", Namespace: "caas-management-test",
		}}
		reconciler := &ClusterOrderReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		}

		roots, check, err := reconciler.managementRouteRootCAs(context.Background(), hc,
			managementRouteEndpoint{service: hypershiftv1beta1.Konnectivity,
				termination: routev1.TLSTerminationPassthrough})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots).To(BeNil())
		Expect(check.ready).To(BeFalse())
		Expect(check.reason).To(Equal(managementPublicationPending))
		Expect(check.message).To(ContainSubstring(hcpKonnectivityServingCAName))
	})

	It("matches management Routes with generated and partially configured hostnames", func() {
		const (
			clusterNamespace = "caas-management-test"
			clusterName      = "cluster-a"
		)
		routeHosts := []string{
			"oauth-generated.example.test",
			"konnectivity-generated.example.test",
			"ignition-generated.example.test",
		}
		routeObjects := []runtime.Object{
			managementRoute(oauthExternalPublicRouteName, routeHosts[0]),
			managementRoute(oauthExternalPrivateRouteName, "oauth-private.example.test"),
			managementRoute(oauthInternalRouteName, "oauth-internal.example.test"),
			managementRoute(konnectivityServerRouteName, routeHosts[1]),
			managementRoute(ignitionServerRouteName, routeHosts[2]),
		}
		testCases := []struct {
			name            string
			configuredHosts map[hypershiftv1beta1.ServiceType]string
		}{
			{name: "all hostnames generated"},
			{
				name: "partial explicit hostnames",
				configuredHosts: map[hypershiftv1beta1.ServiceType]string{
					hypershiftv1beta1.OAuthServer: routeHosts[0],
					hypershiftv1beta1.Ignition:    routeHosts[2],
				},
			},
		}

		for _, testCase := range testCases {
			By(testCase.name)
			services := make([]hypershiftv1beta1.ServicePublishingStrategyMapping, 0, 4)
			for _, serviceType := range []hypershiftv1beta1.ServiceType{
				hypershiftv1beta1.OAuthServer,
				hypershiftv1beta1.OIDC,
				hypershiftv1beta1.Konnectivity,
				hypershiftv1beta1.Ignition,
			} {
				services = append(services, hypershiftv1beta1.ServicePublishingStrategyMapping{
					Service: serviceType,
					ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
						Type: hypershiftv1beta1.Route,
						Route: &hypershiftv1beta1.RoutePublishingStrategy{
							Hostname: testCase.configuredHosts[serviceType],
						},
					},
				})
			}
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: clusterNamespace},
				Spec:       hypershiftv1beta1.HostedClusterSpec{Services: services},
			}
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(routeObjects...).Build()
			reconciler := &ClusterOrderReconciler{Client: k8sClient}

			endpoints, check, err := reconciler.managementRouteEndpoints(context.Background(), hc)
			Expect(err).NotTo(HaveOccurred())
			Expect(check.ready).To(BeTrue())
			hosts := make([]string, 0, len(endpoints))
			for _, endpoint := range endpoints {
				hosts = append(hosts, endpoint.host)
			}
			Expect(hosts).To(ConsistOf(routeHosts))
		}
	})

	It("does not treat unrelated Routes as management Routes", func() {
		const (
			clusterNamespace = "caas-management-test"
			clusterName      = "cluster-a"
		)
		services := make([]hypershiftv1beta1.ServicePublishingStrategyMapping, 0, 4)
		for _, serviceType := range []hypershiftv1beta1.ServiceType{
			hypershiftv1beta1.OAuthServer,
			hypershiftv1beta1.OIDC,
			hypershiftv1beta1.Konnectivity,
			hypershiftv1beta1.Ignition,
		} {
			services = append(services, hypershiftv1beta1.ServicePublishingStrategyMapping{
				Service: serviceType,
				ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
					Type:  hypershiftv1beta1.Route,
					Route: &hypershiftv1beta1.RoutePublishingStrategy{},
				},
			})
		}
		hc := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: clusterNamespace},
			Spec:       hypershiftv1beta1.HostedClusterSpec{Services: services},
		}
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
		objects := []runtime.Object{
			managementRoute("metrics", "metrics.example.test"),
			managementRoute("monitoring", "monitoring.example.test"),
			managementRoute("console", "console.example.test"),
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
		reconciler := &ClusterOrderReconciler{Client: k8sClient}

		endpoints, check, err := reconciler.managementRouteEndpoints(context.Background(), hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.ready).To(BeFalse())
		Expect(check.reason).To(Equal(managementPublicationPending))
		Expect(check.message).To(ContainSubstring("OAuthServer"))
		Expect(endpoints).To(BeNil())
	})
})

type closeErrorConn struct {
	net.Conn
	closeErr error
}

func (c closeErrorConn) Close() error {
	_ = c.Conn.Close()
	return c.closeErr
}

func managementRoute(name, host string, terminations ...routev1.TLSTerminationType) *unstructured.Unstructured {
	termination := routev1.TLSTerminationPassthrough
	if len(terminations) > 0 {
		termination = terminations[0]
	}
	route := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": name, "namespace": "caas-management-test-cluster-a"},
		"spec": map[string]interface{}{
			"host": host,
			"tls":  map[string]interface{}{"termination": string(termination)},
		},
		"status": map[string]interface{}{
			"ingress": []interface{}{map[string]interface{}{
				"conditions": []interface{}{map[string]interface{}{"type": "Admitted", "status": "True"}},
			}},
		},
	}}
	route.SetGroupVersionKind(schema.GroupVersionKind{Group: routeGroup, Version: routeVersion, Kind: routeKind})
	return route
}

func managementTestCA(commonName string) (*x509.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	certificate, err := x509.ParseCertificate(der)
	Expect(err).NotTo(HaveOccurred())
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func managementRootPoolIncludes(roots *x509.CertPool, certificate *x509.Certificate) bool {
	if roots == nil || certificate == nil {
		return false
	}
	_, err := certificate.Verify(x509.VerifyOptions{Roots: roots})
	return err == nil
}

func managementKubeconfig(apiHost string, caData []byte) ([]byte, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate kubeconfig fixture token: %w", err)
	}
	data, err := clientcmd.Write(api.Config{
		CurrentContext: "test",
		Clusters: map[string]*api.Cluster{
			"test": {
				Server:                   fmt.Sprintf("https://%s:6443", apiHost),
				CertificateAuthorityData: caData,
			},
		},
		Contexts: map[string]*api.Context{
			"test": {Cluster: "test", AuthInfo: "test"},
		},
		AuthInfos: map[string]*api.AuthInfo{"test": {
			Token: base64.RawURLEncoding.EncodeToString(token),
		}},
	})
	return data, err
}
