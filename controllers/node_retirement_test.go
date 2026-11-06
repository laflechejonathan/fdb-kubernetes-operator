/*
 * node_retirement_test.go
 *
 * This source file is part of the FoundationDB open source project
 *
 * Copyright 2024 Apple Inc. and the FoundationDB project authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package controllers

import (
	"context"
	"time"

	fdbv1beta2 "github.com/FoundationDB/fdb-kubernetes-operator/v2/api/v1beta2"
	"github.com/FoundationDB/fdb-kubernetes-operator/v2/internal"
	"github.com/FoundationDB/fdb-kubernetes-operator/v2/internal/replacements"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("node label taint replacement", func() {
	const key = "example.com/retiring"
	var cluster *fdbv1beta2.FoundationDBCluster
	var group *fdbv1beta2.ProcessGroupStatus
	var node *corev1.Node
	var pod *corev1.Pod
	ctx := context.Background()
	observe := func() {
		Expect(k8sClient.Update(ctx, node)).To(Succeed())
		Expect(
			updateTaintCondition(ctx, clusterReconciler, cluster, pod, group, testLogger),
		).To(Succeed())
	}
	mature := func() {
		observe()
		group.UpdateConditionTime(
			fdbv1beta2.NodeTaintDetected,
			time.Now().Add(-11*time.Minute).Unix(),
		)
		observe()
		group.UpdateConditionTime(
			fdbv1beta2.NodeTaintReplacing,
			time.Now().Add(-31*time.Minute).Unix(),
		)
	}
	replace := func() bool {
		status := &fdbv1beta2.FoundationDBStatus{}
		changed, _ := replacements.ReplaceFailedProcessGroups(testLogger, cluster, status, true)
		return changed
	}
	BeforeEach(func() {
		cluster = internal.CreateDefaultCluster()
		cluster.Spec.AutomationOptions.Replacements.Enabled = new(true)
		cluster.Spec.AutomationOptions.Replacements.MaxConcurrentReplacements = new(1)
		cluster.Spec.AutomationOptions.Replacements.FaultDomainBasedReplacements = new(false)
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementTimeSeconds = new(1800)
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions = []fdbv1beta2.TaintReplacementOption{
			{
				Key: ptr.To(key), DurationInSeconds: ptr.To[int64](600), MatchNodeLabels: true,
				ProcessClasses: []fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassStateless},
			},
		}
		Expect(internal.NormalizeClusterSpec(cluster, internal.DeprecationOptions{})).To(Succeed())
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "retiring", Labels: map[string]string{key: ""}},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		group = internal.GetProcessGroup(cluster, fdbv1beta2.ProcessClassStateless, 1)
		group.ProcessGroupConditions = nil
		group.FaultDomain = "retiring"
		group.Addresses = []string{"10.0.0.1"}
		cluster.Status.ProcessGroups = []*fdbv1beta2.ProcessGroupStatus{group}
		var err error
		pod, err = internal.GetPod(cluster, group)
		Expect(err).NotTo(HaveOccurred())
		pod.Spec.NodeName = node.Name
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	})
	It(
		"uses both taint delays and persists timestamps across repeated observations and restarts",
		func() {
			observe()
			Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).NotTo(BeNil())
			Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).To(BeNil())
			Expect(replace()).To(BeFalse())
			detected := time.Now().Add(-11 * time.Minute).Unix()
			group.UpdateConditionTime(fdbv1beta2.NodeTaintDetected, detected)
			cluster = cluster.DeepCopy()
			group = cluster.Status.ProcessGroups[0]
			observe()
			Expect(*group.GetConditionTime(fdbv1beta2.NodeTaintDetected)).To(Equal(detected))
			Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).NotTo(BeNil())
			replacing := *group.GetConditionTime(fdbv1beta2.NodeTaintReplacing)
			observe()
			Expect(*group.GetConditionTime(fdbv1beta2.NodeTaintReplacing)).To(Equal(replacing))
			Expect(replace()).To(BeFalse())
			group.UpdateConditionTime(
				fdbv1beta2.NodeTaintReplacing,
				time.Now().Add(-31*time.Minute).Unix(),
			)
			Expect(replace()).To(BeTrue())
			Expect(group.IsMarkedForRemoval()).To(BeTrue())
			Expect(group.ExclusionSkipped).To(BeFalse())
			Expect(replace()).To(BeFalse())
			node.Labels = nil
			observe()
			Expect(group.IsMarkedForRemoval()).To(BeTrue())
		},
	)
	It("does not select label candidates through spec replacement", func() {
		mature()
		changed, err := replacements.ReplaceMisconfiguredProcessGroups(ctx,
			clusterReconciler.PodLifecycleManager, clusterReconciler, testLogger, cluster, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
		Expect(group.IsMarkedForRemoval()).To(BeFalse())
	})
	DescribeTable(
		"honors automatic replacement controls",
		func(enabled bool, limit int, faultLimit int, expected bool) {
			mature()
			cluster.Spec.AutomationOptions.Replacements.Enabled = new(enabled)
			cluster.Spec.AutomationOptions.Replacements.MaxConcurrentReplacements = new(limit)
			cluster.Spec.AutomationOptions.Replacements.MaxFaultDomainsWithTaintedProcessGroups = new(
				intstr.FromInt(faultLimit),
			)
			Expect(replace()).To(Equal(expected))
		},
		Entry("disabled", false, 1, 1, false),
		Entry("zero concurrency", true, 0, 1, false),
		Entry("eligible", true, 1, 1, true),
	)
	It("suppresses label replacements when too many fault domains are affected", func() {
		mature()
		other := group.DeepCopy()
		other.ProcessGroupID = "stateless-2"
		other.FaultDomain = "other-node"
		cluster.Status.ProcessGroups = append(cluster.Status.ProcessGroups, other)
		cluster.Spec.AutomationOptions.Replacements.MaxFaultDomainsWithTaintedProcessGroups = new(
			intstr.FromInt(1),
		)
		Expect(replace()).To(BeFalse())
		Expect(group.IsMarkedForRemoval()).To(BeFalse())
		Expect(other.IsMarkedForRemoval()).To(BeFalse())
	})
	It("waits when an ongoing replacement consumes the automatic budget", func() {
		mature()
		other := internal.GetProcessGroup(cluster, fdbv1beta2.ProcessClassStateless, 2)
		other.FaultDomain = group.FaultDomain
		other.MarkForRemoval()
		cluster.Status.ProcessGroups = append(cluster.Status.ProcessGroups, other)
		Expect(replace()).To(BeFalse())
		Expect(group.IsMarkedForRemoval()).To(BeFalse())
	})
	It(
		"preserves the label timer when a real taint is removed and never writes node taints",
		func() {
			mature()
			detected := *group.GetConditionTime(fdbv1beta2.NodeTaintDetected)
			node.Spec.Taints = []corev1.Taint{{Key: key}}
			observe()
			node.Spec.Taints = nil
			observe()
			Expect(*group.GetConditionTime(fdbv1beta2.NodeTaintDetected)).To(Equal(detected))
			stored := &corev1.Node{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: node.Name}, stored)).To(Succeed())
			Expect(stored.Spec.Taints).To(BeEmpty())
			cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions = nil
			observe()
			Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).To(BeNil())
			Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).To(BeNil())
		},
	)
	It("defers selection while the database is unavailable", func() {
		mature()
		result := (replaceFailedProcessGroups{}).reconcile(ctx, clusterReconciler, cluster,
			&fdbv1beta2.FoundationDBStatus{}, testLogger)
		Expect(result).NotTo(BeNil())
		Expect(group.IsMarkedForRemoval()).To(BeFalse())
	})
	It("clears withdrawn signals but retains conditions while a real taint still matches", func() {
		mature()
		node.Spec.Taints = []corev1.Taint{{Key: key, Effect: corev1.TaintEffectNoSchedule}}
		node.Labels = nil
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).NotTo(BeNil())
		node.Spec.Taints = nil
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).To(BeNil())
		Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).To(BeNil())
		Expect(replace()).To(BeFalse())
	})
	DescribeTable(
		"applies class filters equally to taints and labels",
		func(label bool, classes []fdbv1beta2.ProcessClass, expected bool) {
			cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions[0].ProcessClasses = classes
			if !label {
				node.Labels = nil
				node.Spec.Taints = []corev1.Taint{{Key: key}}
			}
			observe()
			Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected) != nil).To(Equal(expected))
		},
		Entry(
			"label selected",
			true,
			[]fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassStateless},
			true,
		),
		Entry(
			"taint selected",
			false,
			[]fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassStateless},
			true,
		),
		Entry("label excluded", true, []fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassLog}, false),
		Entry(
			"taint excluded",
			false,
			[]fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassLog},
			false,
		),
		Entry("label all classes", true, nil, true),
		Entry("taint all classes", false, []fdbv1beta2.ProcessClass{}, true),
	)
	It("clears conditions when the class filter stops matching", func() {
		mature()
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions[0].ProcessClasses = []fdbv1beta2.ProcessClass{
			fdbv1beta2.ProcessClassLog,
		}
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).To(BeNil())
		Expect(group.GetCondition(fdbv1beta2.NodeTaintReplacing)).To(BeNil())
	})
	It("falls back to wildcard options when an exact entry excludes the class", func() {
		node.Spec.Taints = []corev1.Taint{{Key: key}}
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions[0].ProcessClasses = []fdbv1beta2.ProcessClass{
			fdbv1beta2.ProcessClassLog,
		}
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions = append(
			cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions,
			fdbv1beta2.TaintReplacementOption{
				Key:               new("*"),
				DurationInSeconds: ptr.To[int64](0),
			},
		)
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).NotTo(BeNil())
	})
	DescribeTable(
		"uses the same wildcard matching for labels and taints",
		func(label bool, exact bool, optIn bool, classes []fdbv1beta2.ProcessClass, expected bool) {
			if !label {
				node.Labels = nil
				node.Spec.Taints = []corev1.Taint{{Key: key}}
			}
			options := []fdbv1beta2.TaintReplacementOption{{
				Key: new("*"), DurationInSeconds: ptr.To[int64](600), MatchNodeLabels: optIn,
				ProcessClasses: classes,
			}}
			if exact {
				// An exact option that does not opt into labels suppresses wildcard label matching.
				options = append(options, fdbv1beta2.TaintReplacementOption{
					Key: ptr.To(key), DurationInSeconds: ptr.To[int64](600),
				})
			}
			cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions = options
			observe()
			Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected) != nil).To(Equal(expected))
		},
		Entry("wildcard label", true, false, true, nil, true),
		Entry("wildcard taint", false, false, false, nil, true),
		Entry("label opt-in required", true, false, false, nil, false),
		Entry("exact label opt-out wins", true, true, true, nil, false),
		Entry("exact real taint still matches", false, true, true, nil, true),
		Entry(
			"wildcard label class filter",
			true,
			false,
			true,
			[]fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassLog},
			false,
		),
		Entry(
			"wildcard taint class filter",
			false,
			false,
			true,
			[]fdbv1beta2.ProcessClass{fdbv1beta2.ProcessClassLog},
			false,
		),
	)
	It("requires label opt-in and ignores unrelated labels and cordons", func() {
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions[0].MatchNodeLabels = false
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).To(BeNil())
		cluster.Spec.AutomationOptions.Replacements.TaintReplacementOptions[0].MatchNodeLabels = true
		node.Labels = map[string]string{"unrelated": "true"}
		node.Spec.Unschedulable = true
		observe()
		Expect(group.GetCondition(fdbv1beta2.NodeTaintDetected)).To(BeNil())
	})
})
