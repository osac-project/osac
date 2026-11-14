// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Native controller ownership", func() {
	DescribeTable(
		"checks the complete namespaced controller identity",
		func(
			change func(*corev1.Secret, *corev1.ConfigMap), want bool,
		) {
			parent := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "parent", UID: "original"}}
			kind := corev1.SchemeGroupVersion.WithKind("ConfigMap")

			child := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "output", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(parent, kind)},
			}}
			if change != nil {
				change(child, parent)
			}

			Expect(MatchesController(child, parent, kind)).To(Equal(want))
		},
		Entry("original controller", nil, true),
		Entry("another namespace", func(child *corev1.Secret, _ *corev1.ConfigMap) { child.Namespace = "foreign" }, false),
		Entry("a replacement parent", func(_ *corev1.Secret, parent *corev1.ConfigMap) { parent.UID = "replacement" }, false),
		Entry("an unobserved parent", func(_ *corev1.Secret, parent *corev1.ConfigMap) { parent.UID = "" }, false),
		Entry(
			"another API",
			func(child *corev1.Secret, _ *corev1.ConfigMap) { child.OwnerReferences[0].APIVersion = "foreign/v1" },
			false,
		),
		Entry(
			"another kind",
			func(child *corev1.Secret, _ *corev1.ConfigMap) { child.OwnerReferences[0].Kind = "Secret" },
			false,
		),
		Entry(
			"another name",
			func(child *corev1.Secret, _ *corev1.ConfigMap) { child.OwnerReferences[0].Name = "another" },
			false,
		),
		Entry(
			"a non-controller owner",
			func(child *corev1.Secret, _ *corev1.ConfigMap) { child.OwnerReferences[0].Controller = nil },
			false,
		),
		Entry("an unowned child", func(child *corev1.Secret, _ *corev1.ConfigMap) { child.OwnerReferences = nil }, false),
	)

	It("distinguishes a controller from another native owner", func() {
		controller := metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "controller",
			UID:        "controller",
			Controller: new(true),
		}
		source := metav1.OwnerReference{APIVersion: "v1", Kind: "Secret", Name: "source", UID: "source"}
		child := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{source, controller}}}
		Expect(Owner(child, "ConfigMap")).To(Equal(&controller))
		Expect(Owner(child, "Secret")).To(BeNil())
		Expect(OwnerReference(child, "Secret")).To(Equal(&source))
		Expect(OwnerReference(child, "Namespace")).To(BeNil())
	})
})
