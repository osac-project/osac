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
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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

// TestToolHubTokenFiles embeds only the selected user's effective bearer credential.
func TestToolHubTokenFiles(t *testing.T) {
	for _, test := range []struct {
		name, inline, file, want string
		missing, invalid         bool
	}{
		{name: "file only", file: "selected-token\n", want: "selected-token"},
		{name: "file overrides inline", inline: "old-token", file: "selected-token\n", want: "selected-token"},
		{name: "missing file uses inline", inline: "selected-token", missing: true, want: "selected-token"},
		{name: "empty file uses inline", inline: "selected-token", want: "selected-token"},
		{name: "missing file only", missing: true, invalid: true},
		{name: "empty file only", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			directory := t.TempDir()
			if !test.missing {
				g.Expect(os.WriteFile(filepath.Join(directory, "token"), []byte(test.file), 0o600)).To(Succeed())
			}
			configuration := clientcmdapi.Config{
				CurrentContext: "selected",
				Contexts:       map[string]*clientcmdapi.Context{"selected": {Cluster: "selected", AuthInfo: "selected"}},
				Clusters:       map[string]*clientcmdapi.Cluster{"selected": {Server: "https://selected.invalid"}},
				AuthInfos: map[string]*clientcmdapi.AuthInfo{
					"selected": {Token: test.inline, TokenFile: "token"},
					"other":    {TokenFile: "/unselected/missing-token"},
				},
			}
			file := filepath.Join(directory, "config")
			g.Expect(clientcmd.WriteToFile(configuration, file)).To(Succeed())
			rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: file}
			loaded, err := rules.Load()
			g.Expect(err).NotTo(HaveOccurred())
			original := loaded.DeepCopy()
			tool := &Tool{kubeConfig: clientcmd.NewNonInteractiveClientConfig(*loaded, "", nil, rules)}

			data, err := tool.hubKubeconfig()
			g.Expect(*loaded).To(Equal(*original))
			if test.invalid {
				g.Expect(err).To(HaveOccurred())
				g.Expect(data).To(BeEmpty())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			hub, err := clientcmd.Load(data)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hub.AuthInfos).To(HaveLen(1))
			g.Expect(hub.AuthInfos["selected"].Token).To(Equal(test.want))
			g.Expect(hub.AuthInfos["selected"].TokenFile).To(BeEmpty())
			host, err := tool.kubeConfig.RawConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(host).To(Equal(*original))
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
