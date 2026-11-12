// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package it

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/clientcmd"
)

// TestToolKubeconfig checks local client and hub configuration without deployed services.
func TestToolKubeconfig(t *testing.T) {
	for _, mode := range []string{"default", "single file", "merged files", "merged missing file"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("KIND_CLUSTER", "selected")
			directory := filepath.Join(home, ".kube")
			g.Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
			contextFile := filepath.Join(directory, "context.yaml")
			clusterFile := filepath.Join(directory, "selected-kind.kubeconfig")
			contextData := []byte(`apiVersion: v1
kind: Config
current-context: selected
contexts:
- name: selected
  context:
    cluster: selected
    user: selected
- name: unrelated
  context:
    cluster: unrelated
    user: unrelated
clusters:
- name: unrelated
  cluster:
    server: https://unrelated.invalid
users:
- name: unrelated
  user:
    token: unrelated-token
`)
			clusterData := []byte(`apiVersion: v1
kind: Config
clusters:
- name: selected
  cluster:
    server: https://selected.invalid
users:
- name: selected
  user:
    token: selected-token
`)
			g.Expect(os.WriteFile(clusterFile, clusterData, 0o600)).To(Succeed())
			if mode == "merged files" || mode == "merged missing file" {
				g.Expect(os.WriteFile(contextFile, contextData, 0o600)).To(Succeed())
				files := []string{contextFile, clusterFile}
				if mode == "merged missing file" {
					files = append(files, filepath.Join(home, "missing"))
				}
				t.Setenv("KUBECONFIG", strings.Join(files, string(os.PathListSeparator)))
			} else {
				contextConfig, err := clientcmd.Load(contextData)
				g.Expect(err).NotTo(HaveOccurred())
				clusterConfig, err := clientcmd.Load(clusterData)
				g.Expect(err).NotTo(HaveOccurred())
				contextConfig.Clusters["selected"] = clusterConfig.Clusters["selected"]
				contextConfig.AuthInfos["selected"] = clusterConfig.AuthInfos["selected"]
				g.Expect(clientcmd.WriteToFile(*contextConfig, clusterFile)).To(Succeed())
				t.Setenv("KUBECONFIG", "")
				if mode == "single file" {
					t.Setenv("KUBECONFIG", clusterFile)
				}
			}

			tool := &Tool{}
			g.Expect(tool.openKubernetes()).To(Succeed())
			configuration, err := tool.kubeConfig.ClientConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(configuration.Host).To(Equal("https://selected.invalid"))
			g.Expect(configuration.BearerToken).To(Equal("selected-token"))

			data, err := tool.hubKubeconfig()
			g.Expect(err).NotTo(HaveOccurred())
			hub, err := clientcmd.Load(data)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hub.CurrentContext).To(Equal("selected"))
			g.Expect(hub.Contexts).To(HaveLen(1))
			g.Expect(hub.Clusters).To(HaveLen(1))
			g.Expect(hub.AuthInfos).To(HaveLen(1))
			g.Expect(hub.Clusters["selected"].Server).To(Equal("https://kubernetes.default.svc"))
			g.Expect(hub.AuthInfos["selected"].Token).To(Equal("selected-token"))
			g.Expect(data).NotTo(ContainSubstring("unrelated-token"))
			configuration, err = tool.kubeConfig.ClientConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(configuration.Host).To(Equal("https://selected.invalid"))
		})
	}
}

// TestToolKubeconfigRejectsEmptySelection rejects unusable explicit files before creating clients.
func TestToolKubeconfigRejectsEmptySelection(t *testing.T) {
	for _, mode := range []string{"missing", "empty"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			file := filepath.Join(t.TempDir(), "kubeconfig")
			if mode == "empty" {
				g.Expect(os.WriteFile(file, []byte("apiVersion: v1\nkind: Config\n"), 0o600)).To(Succeed())
			}
			t.Setenv("KUBECONFIG", file)
			tool := &Tool{}
			g.Expect(tool.openKubernetes()).NotTo(Succeed())
			g.Expect(tool.kubeClient).To(BeNil())
			g.Expect(tool.kubeClientSet).To(BeNil())
		})
	}
}
