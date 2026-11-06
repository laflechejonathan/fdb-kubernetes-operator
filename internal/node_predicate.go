/*
 * node_predicate.go
 *
 * This source file is part of the FoundationDB open source project
 *
 * Copyright 2018-2026 Apple Inc. and the FoundationDB project authors
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
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var _ predicate.Predicate = (*NodeTaintChangedPredicate)(nil)

// NodeTaintChangedPredicate enqueues changes to node taints or labels.
type NodeTaintChangedPredicate struct {
	Logger logr.Logger
}

// Create implements Predicate.
func (n NodeTaintChangedPredicate) Create(_ event.CreateEvent) bool {
	return false
}

// Delete implements Predicate.
func (n NodeTaintChangedPredicate) Delete(_ event.DeleteEvent) bool {
	return false
}

// Update returns true when taints or label keys have changed; label values are ignored.
func (n NodeTaintChangedPredicate) Update(event event.UpdateEvent) bool {
	if event.ObjectOld == nil || event.ObjectNew == nil {
		return false
	}

	oldNode, ok := event.ObjectOld.(*corev1.Node)
	if !ok {
		return false
	}

	newNode, ok := event.ObjectNew.(*corev1.Node)
	if !ok {
		return false
	}

	taintsChanged := !equality.Semantic.DeepEqual(oldNode.Spec.Taints, newNode.Spec.Taints)
	if taintsChanged {
		n.Logger.V(1).
			Info("Node taints have changed", "node", oldNode.Name, "taintsChanged", taintsChanged, "oldTaints", oldNode.Spec.Taints, "newTaints", newNode.Spec.Taints)
	}

	if taintsChanged || len(oldNode.Labels) != len(newNode.Labels) {
		return true
	}
	for key := range oldNode.Labels {
		if _, present := newNode.Labels[key]; !present {
			return true
		}
	}
	return false
}

// Generic implements Predicate.
func (n NodeTaintChangedPredicate) Generic(_ event.GenericEvent) bool {
	return false
}
