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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// Children live in their own namespace, and owner references can't cross namespaces
// (or point from a cluster-scoped Namespace to a namespaced owner). These labels record
// the owning PreviewEnvironment instead, for both watches and deletion.
const (
	ownerNameLabel      = "preview.seppys.dev/name"
	ownerNamespaceLabel = "preview.seppys.dev/namespace"
)

// cleanupFinalizer blocks deletion of a PreviewEnvironment until its namespace is deleted.
const cleanupFinalizer = "preview.seppys.dev/cleanup"

// setOwnerLabels marks obj as managed by environment, keeping any other labels it has.
func setOwnerLabels(obj metav1.Object, environment *previewv1alpha1.PreviewEnvironment) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ownerNameLabel] = environment.Name
	labels[ownerNamespaceLabel] = environment.Namespace
	obj.SetLabels(labels)
}

// isOwnedBy reports whether obj carries environment's owner labels.
func isOwnedBy(obj metav1.Object, environment *previewv1alpha1.PreviewEnvironment) bool {
	labels := obj.GetLabels()
	return labels[ownerNameLabel] == environment.Name && labels[ownerNamespaceLabel] == environment.Namespace
}

// environmentForObject maps a child event to a reconcile request for its owning
// PreviewEnvironment. Objects without owner labels aren't ours and are ignored.
func environmentForObject(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	name, namespace := labels[ownerNameLabel], labels[ownerNamespaceLabel]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}}
}
