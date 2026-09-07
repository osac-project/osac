/*
Copyright 2025.

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

package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/internal/controller"

	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Main Suite")
}

// mockCluster implements cluster.Cluster.Start for testing.
type mockCluster struct {
	cluster.Cluster
	startFunc func(ctx context.Context) error
}

func (m *mockCluster) Start(ctx context.Context) error {
	return m.startFunc(ctx)
}

// mockProvider implements both multicluster.Provider and multicluster.ProviderRunnable.
type mockProvider struct {
	multicluster.Provider
	startFunc func(ctx context.Context, mgr multicluster.Aware) error
}

func (m *mockProvider) Start(ctx context.Context, mgr multicluster.Aware) error {
	return m.startFunc(ctx, mgr)
}

// mockManager implements mcmanager.Manager.Start for testing.
type mockManager struct {
	mcmanager.Manager
	startFunc func(ctx context.Context) error
}

func (m *mockManager) Start(ctx context.Context) error {
	return m.startFunc(ctx)
}

var _ = Describe("ignoreCanceled", func() {
	It("should return nil for context.Canceled", func() {
		Expect(ignoreCanceled(context.Canceled)).To(Succeed())
	})

	It("should preserve wrapped context.Canceled (not a pure cancellation)", func() {
		wrapped := errors.Join(errors.New("something"), context.Canceled)
		Expect(ignoreCanceled(wrapped)).NotTo(Succeed())
	})

	It("should return nil for nil error", func() {
		Expect(ignoreCanceled(nil)).To(Succeed())
	})

	It("should preserve real errors", func() {
		realErr := errors.New("connection refused")
		Expect(ignoreCanceled(realErr)).To(Equal(realErr))
	})
})

var _ = Describe("clusterOrderStallThresholdsFromEnv", func() {
	stallThresholdEnvironmentVariables := []string{
		envClusterPreparingInfrastructureStallThreshold,
		envClusterControlPlaneStartingStallThreshold,
		envClusterWorkersJoiningStallThreshold,
		envClusterWorkersJoiningStallThresholdOverrides,
	}

	BeforeEach(func() {
		type environmentValue struct {
			value string
			set   bool
		}

		originalValues := make(map[string]environmentValue, len(stallThresholdEnvironmentVariables))
		for _, environmentVariable := range stallThresholdEnvironmentVariables {
			value, set := os.LookupEnv(environmentVariable)
			originalValues[environmentVariable] = environmentValue{value: value, set: set}
			Expect(os.Unsetenv(environmentVariable)).To(Succeed())
		}
		DeferCleanup(func() {
			for environmentVariable, originalValue := range originalValues {
				if originalValue.set {
					Expect(os.Setenv(environmentVariable, originalValue.value)).To(Succeed())
					continue
				}
				Expect(os.Unsetenv(environmentVariable)).To(Succeed())
			}
		})
	})

	It("uses production defaults when thresholds are not configured", func() {
		thresholds := clusterOrderStallThresholdsFromEnv()

		Expect(thresholds).To(Equal(controller.DefaultClusterOrderStallThresholds()))
	})

	It("accepts configured thresholds and valid per-host-type worker overrides", func() {
		Expect(os.Setenv(envClusterPreparingInfrastructureStallThreshold, "10m")).To(Succeed())
		Expect(os.Setenv(envClusterControlPlaneStartingStallThreshold, "25m")).To(Succeed())
		Expect(os.Setenv(envClusterWorkersJoiningStallThreshold, "15m")).To(Succeed())
		Expect(os.Setenv(envClusterWorkersJoiningStallThresholdOverrides,
			`{"fast":"5m","slow":"40m","invalid":"not-a-duration","zero":"0s"}`)).To(Succeed())

		thresholds := clusterOrderStallThresholdsFromEnv()

		Expect(thresholds.PreparingInfrastructure).To(Equal(10 * time.Minute))
		Expect(thresholds.ControlPlaneStarting).To(Equal(25 * time.Minute))
		Expect(thresholds.WorkersJoining).To(Equal(15 * time.Minute))
		Expect(thresholds.WorkersJoiningByHostType).To(Equal(map[string]time.Duration{
			"fast": 5 * time.Minute,
			"slow": 40 * time.Minute,
		}))
	})

	It("keeps defaults when the worker override configuration is malformed", func() {
		Expect(os.Setenv(envClusterWorkersJoiningStallThresholdOverrides, "not-json")).To(Succeed())

		thresholds := clusterOrderStallThresholdsFromEnv()

		Expect(thresholds).To(Equal(controller.DefaultClusterOrderStallThresholds()))
	})
})

var _ = Describe("startComponents", func() {
	It("should succeed with manager only (no remote cluster or provider)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				cancel()
				return context.Canceled
			},
		}

		Expect(startComponents(ctx, nil, nil, mgr)).To(Succeed())
	})

	It("should propagate manager errors", func() {
		mgrErr := errors.New("manager failed")
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				return mgrErr
			},
		}

		Expect(startComponents(context.Background(), nil, nil, mgr)).To(MatchError(mgrErr))
	})

	It("should propagate remote cluster errors and cancel manager", func() {
		clusterErr := errors.New("remote cluster unreachable")
		cl := &mockCluster{
			startFunc: func(ctx context.Context) error {
				return clusterErr
			},
		}
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}

		Expect(startComponents(context.Background(), cl, nil, mgr)).To(MatchError(clusterErr))
	})

	It("should propagate remote provider errors and cancel cluster and manager", func() {
		providerErr := errors.New("provider failed to engage")
		cl := &mockCluster{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}
		prov := &mockProvider{
			startFunc: func(ctx context.Context, mgr multicluster.Aware) error {
				return providerErr
			},
		}
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}

		Expect(startComponents(context.Background(), cl, prov, mgr)).To(MatchError(providerErr))
	})

	It("should shut down all components gracefully on context cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())

		clusterStopped := make(chan struct{})
		providerStopped := make(chan struct{})
		mgrStopped := make(chan struct{})

		cl := &mockCluster{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				close(clusterStopped)
				return ctx.Err()
			},
		}
		prov := &mockProvider{
			startFunc: func(ctx context.Context, mgr multicluster.Aware) error {
				<-ctx.Done()
				close(providerStopped)
				return ctx.Err()
			},
		}
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				close(mgrStopped)
				return ctx.Err()
			},
		}

		done := make(chan error, 1)
		go func() {
			done <- startComponents(ctx, cl, prov, mgr)
		}()

		cancel()

		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		Eventually(clusterStopped).Should(BeClosed())
		Eventually(providerStopped).Should(BeClosed())
		Eventually(mgrStopped).Should(BeClosed())
	})

	It("should stop provider and manager when cluster fails", func() {
		clusterErr := errors.New("cluster connection lost")

		providerStopped := make(chan struct{})
		mgrStopped := make(chan struct{})

		cl := &mockCluster{
			startFunc: func(ctx context.Context) error {
				time.Sleep(10 * time.Millisecond)
				return clusterErr
			},
		}
		prov := &mockProvider{
			startFunc: func(ctx context.Context, mgr multicluster.Aware) error {
				<-ctx.Done()
				close(providerStopped)
				return ctx.Err()
			},
		}
		mgr := &mockManager{
			startFunc: func(ctx context.Context) error {
				<-ctx.Done()
				close(mgrStopped)
				return ctx.Err()
			},
		}

		Expect(startComponents(context.Background(), cl, prov, mgr)).To(MatchError(clusterErr))
		Eventually(providerStopped, 5*time.Second).Should(BeClosed())
		Eventually(mgrStopped, 5*time.Second).Should(BeClosed())
	})
})

var _ = Describe("parseVendorControllers", func() {
	It("returns an empty map for empty input", func() {
		m, err := parseVendorControllers("")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeEmpty())
	})

	It("parses a single backend=endpoint pair", func() {
		m, err := parseVendorControllers("vast=vast-csi-controller.osac-csi-backends.svc:50051")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(map[string]string{
			"vast": "vast-csi-controller.osac-csi-backends.svc:50051",
		}))
	})

	It("parses multiple comma-separated pairs", func() {
		m, err := parseVendorControllers("vast=vast.svc:50051,netapp=netapp.svc:50052")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(map[string]string{
			"vast":   "vast.svc:50051",
			"netapp": "netapp.svc:50052",
		}))
	})

	It("trims whitespace around pairs, backends, and endpoints", func() {
		m, err := parseVendorControllers("  vast = vast.svc:50051 ,  netapp=netapp.svc:50052  ")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(map[string]string{
			"vast":   "vast.svc:50051",
			"netapp": "netapp.svc:50052",
		}))
	})

	It("skips empty pairs from trailing or doubled commas", func() {
		m, err := parseVendorControllers("vast=vast.svc:50051,,")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(map[string]string{"vast": "vast.svc:50051"}))
	})

	It("preserves ports in endpoints containing an equals sign", func() {
		// SplitN with limit 2 keeps everything after the first '=' as the endpoint.
		m, err := parseVendorControllers("vast=host:50051/path?a=b")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(map[string]string{"vast": "host:50051/path?a=b"}))
	})

	It("errors on a pair missing the equals separator", func() {
		_, err := parseVendorControllers("vast")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected format backend=endpoint"))
	})

	It("errors on an empty backend", func() {
		_, err := parseVendorControllers("=vast.svc:50051")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("backend and endpoint must not be empty"))
	})

	It("errors on an empty endpoint", func() {
		_, err := parseVendorControllers("vast=")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("backend and endpoint must not be empty"))
	})
})
