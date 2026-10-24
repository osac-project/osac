package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("remote kubeconfig watcher", func() {
	It("requests an operator restart when the mounted kubeconfig changes", func() {
		path := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
		Expect(os.WriteFile(path, []byte("initial kubeconfig"), 0o600)).To(Succeed())
		initialDigest, err := readRemoteKubeconfigDigest(path)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		changed := make(chan bool, 1)
		go func() {
			changed <- watchRemoteKubeconfig(ctx, path, initialDigest, 10*time.Millisecond)
		}()
		Expect(os.WriteFile(path, []byte("rotated kubeconfig"), 0o600)).To(Succeed())

		select {
		case restart := <-changed:
			Expect(restart).To(BeTrue())
		case <-time.After(time.Second):
			Fail("watcher did not detect the kubeconfig change")
		}
	})

	It("stops without requesting a restart when its context is canceled", func() {
		path := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
		Expect(os.WriteFile(path, []byte("initial kubeconfig"), 0o600)).To(Succeed())
		initialDigest, err := readRemoteKubeconfigDigest(path)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		changed := make(chan bool, 1)
		go func() {
			changed <- watchRemoteKubeconfig(ctx, path, initialDigest, time.Second)
		}()
		cancel()

		select {
		case restart := <-changed:
			Expect(restart).To(BeFalse())
		case <-time.After(time.Second):
			Fail("watcher did not stop after context cancellation")
		}
	})
})
