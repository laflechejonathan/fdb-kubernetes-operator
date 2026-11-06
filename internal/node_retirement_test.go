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

package internal

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var _ = Describe("node retirement events", func() {
	const key = "example.com/retiring"
	It("filters cordons while retaining taint and label triggers", func() {
		old := &corev1.Node{}
		updated := old.DeepCopy()
		updated.Spec.Unschedulable = true
		predicate := NodeTaintChangedPredicate{
			Logger: GinkgoLogr,
		}
		Expect(
			predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}),
		).To(BeFalse())
		updated.Labels = map[string]string{"unrelated": "true"}
		Expect(
			predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}),
		).To(BeTrue())
		updated.Labels[key] = ""
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})).To(BeTrue())
		old = updated.DeepCopy()
		updated.Labels[key] = "new-value"
		Expect(
			predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}),
		).To(BeFalse())
		delete(updated.Labels, key)
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})).To(BeTrue())
		updated.Labels = nil
		updated.Spec.Taints = []corev1.Taint{
			{Key: "unhealthy", Effect: corev1.TaintEffectNoExecute},
		}
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})).To(BeTrue())
	})
})
