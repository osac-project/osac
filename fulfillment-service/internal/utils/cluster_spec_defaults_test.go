/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("ApplyClusterSpecDefaults", func() {
	It("Does nothing when defaults are nil", func() {
		pullSecret := "my-secret"
		spec := privatev1.ClusterSpec_builder{
			PullSecret: &pullSecret,
		}.Build()
		ApplyClusterSpecDefaults(spec, nil)
		Expect(spec.GetPullSecret()).To(Equal("my-secret"))
	})

	It("Does nothing when spec is nil", func() {
		pullSecret := "default-secret"
		defaults := privatev1.ClusterTemplateSpecDefaults_builder{
			PullSecret: &pullSecret,
		}.Build()
		ApplyClusterSpecDefaults(nil, defaults)
	})

	It("Applies all defaults to empty spec", func() {
		pullSecret := "default-pull-secret"
		sshKey := "ssh-rsa AAAA..."
		versionName := "4-22-0"
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		defaults := privatev1.ClusterTemplateSpecDefaults_builder{
			PullSecret:   &pullSecret,
			SshPublicKey: &sshKey,
			VersionName:  &versionName,
			Network: privatev1.ClusterNetwork_builder{
				PodCidr:     &podCidr,
				ServiceCidr: &serviceCidr,
			}.Build(),
		}.Build()

		spec := privatev1.ClusterSpec_builder{}.Build()
		ApplyClusterSpecDefaults(spec, defaults)

		Expect(spec.GetPullSecret()).To(Equal("default-pull-secret"))
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-rsa AAAA..."))
		Expect(spec.GetVersionName()).To(Equal("4-22-0"))
		Expect(spec.GetNetwork().GetPodCidr()).To(Equal("10.128.0.0/14"))
		Expect(spec.GetNetwork().GetServiceCidr()).To(Equal("172.30.0.0/16"))
	})

	It("User values override defaults", func() {
		userPullSecret := "user-pull-secret"
		userSshKey := "ssh-ed25519 user-key"
		userVersion := "4-22-1"
		defaultPullSecret := "default-pull-secret"
		defaultSshKey := "ssh-rsa default-key"
		defaultVersion := "4-22-0"
		defaults := privatev1.ClusterTemplateSpecDefaults_builder{
			PullSecret:   &defaultPullSecret,
			SshPublicKey: &defaultSshKey,
			VersionName:  &defaultVersion,
		}.Build()

		spec := privatev1.ClusterSpec_builder{
			PullSecret:   &userPullSecret,
			SshPublicKey: &userSshKey,
			VersionName:  &userVersion,
		}.Build()
		ApplyClusterSpecDefaults(spec, defaults)

		Expect(spec.GetPullSecret()).To(Equal("user-pull-secret"))
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-ed25519 user-key"))
		Expect(spec.GetVersionName()).To(Equal("4-22-1"))
	})

	It("Merges partial network defaults", func() {
		userPodCidr := "10.200.0.0/14"
		defaultPodCidr := "10.128.0.0/14"
		defaultServiceCidr := "172.30.0.0/16"
		defaults := privatev1.ClusterTemplateSpecDefaults_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr:     &defaultPodCidr,
				ServiceCidr: &defaultServiceCidr,
			}.Build(),
		}.Build()

		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr: &userPodCidr,
			}.Build(),
		}.Build()
		ApplyClusterSpecDefaults(spec, defaults)

		Expect(spec.GetNetwork().GetPodCidr()).To(Equal("10.200.0.0/14"))
		Expect(spec.GetNetwork().GetServiceCidr()).To(Equal("172.30.0.0/16"))
	})

	It("Clones network defaults to prevent shared state", func() {
		podCidr := "10.128.0.0/14"
		defaults := privatev1.ClusterTemplateSpecDefaults_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr: &podCidr,
			}.Build(),
		}.Build()

		spec := privatev1.ClusterSpec_builder{}.Build()
		ApplyClusterSpecDefaults(spec, defaults)

		// Mutating the spec's network should not affect the defaults
		newCidr := "10.200.0.0/14"
		spec.GetNetwork().SetPodCidr(newCidr)
		Expect(defaults.GetNetwork().GetPodCidr()).To(Equal("10.128.0.0/14"))
	})
})

var _ = Describe("ValidateClusterSpecFields", func() {
	It("Returns nil when spec is nil", func() {
		err := ValidateClusterSpecFields(nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Passes with no network", func() {
		spec := privatev1.ClusterSpec_builder{}.Build()
		err := ValidateClusterSpecFields(spec)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Passes with valid CIDRs", func() {
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr:     &podCidr,
				ServiceCidr: &serviceCidr,
			}.Build(),
		}.Build()
		err := ValidateClusterSpecFields(spec)
		Expect(err).ToNot(HaveOccurred())
	})

	It("canonicalizes non-canonical pod and service CIDRs", func() {
		podCidr := "10.128.0.5/14"
		serviceCidr := "172.30.1.0/16"
		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr:     &podCidr,
				ServiceCidr: &serviceCidr,
			}.Build(),
		}.Build()
		err := ValidateClusterSpecFields(spec)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetwork().GetPodCidr()).To(Equal("10.128.0.0/14"))
		Expect(spec.GetNetwork().GetServiceCidr()).To(Equal("172.30.0.0/16"))
	})

	It("Returns error for invalid pod_cidr", func() {
		podCidr := "invalid-cidr"
		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr: &podCidr,
			}.Build(),
		}.Build()
		err := ValidateClusterSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid pod_cidr"))
	})

	It("Returns error for invalid service_cidr", func() {
		serviceCidr := "not-a-cidr"
		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				ServiceCidr: &serviceCidr,
			}.Build(),
		}.Build()
		err := ValidateClusterSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid service_cidr"))
	})
})
