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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// conditionTypeReady reports whether the preview environment serves the requested spec.
const conditionTypeReady = "Ready"

// Reasons for the Ready condition. They are part of the API: other code may compare against them.
const (
	ReasonAvailable                 = "Available"
	ReasonRolloutInProgress         = "RolloutInProgress"
	ReasonRolloutFailed             = "RolloutFailed"
	ReasonDeploymentReconcileFailed = "DeploymentReconcileFailed"
	ReasonServiceReconcileFailed    = "ServiceReconcileFailed"
	ReasonIngressReconcileFailed    = "IngressReconcileFailed"
	ReasonIngressAddressPending     = "IngressAddressPending"
)

// setReadyCondition records the Ready condition on env in memory only; the caller writes the status.
// LastTransitionTime only changes when status flips, so repeated calls with the same values are no-ops.
func setReadyCondition(env *previewv1alpha1.PreviewEnvironment, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: env.Generation,
	})
}

// failReconcile marks env as not Ready because reconciling a child resource failed, writes the
// status, and returns reconcileErr so controller-runtime retries with backoff. A failed status
// write is only logged: reconcileErr is the more useful error to surface and retry on.
func (r *PreviewEnvironmentReconciler) failReconcile(
	ctx context.Context, env *previewv1alpha1.PreviewEnvironment, reason string, reconcileErr error,
) (ctrl.Result, error) {
	setReadyCondition(env, metav1.ConditionFalse, reason, reconcileErr.Error())
	if err := r.Status().Update(ctx, env); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update PreviewEnvironment status")
	}
	return ctrl.Result{}, reconcileErr
}

// setReady derives the Ready condition and URL from the observed children. Ready is True only
// once the rollout is complete and the ingress controller has assigned the Ingress an address.
// The condition is computed first and set once: setting it True and then overriding it with
// False would look like a flip and bump LastTransitionTime on every reconcile.
func setReady(env *previewv1alpha1.PreviewEnvironment, dep *appsv1.Deployment, ing *networkingv1.Ingress) {
	status, reason, message := rolloutState(dep)

	env.Status.URL = ""
	if hasAddress(ing) {
		if len(ing.Spec.Rules) > 0 {
			env.Status.URL = "http://" + ing.Spec.Rules[0].Host
		}
	} else if status == metav1.ConditionTrue {
		status, reason, message = metav1.ConditionFalse, ReasonIngressAddressPending,
			"Waiting for the ingress controller to assign the Ingress an address"
	}

	if status == metav1.ConditionTrue {
		env.Status.DeployedCommitSHA = env.Spec.CommitSHA
	}
	setReadyCondition(env, status, reason, message)
}

// hasAddress reports whether an ingress controller has published an address for ing.
func hasAddress(ing *networkingv1.Ingress) bool {
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.IP != "" || lb.Hostname != "" {
			return true
		}
	}
	return false
}

// rolloutState derives the Ready status, reason and message from the Deployment's rollout state.
// It mirrors `kubectl rollout status`: the rollout is only complete once every replica runs the
// latest pod template and is available, so a pod still serving the previous image never counts.
func rolloutState(dep *appsv1.Deployment) (metav1.ConditionStatus, string, string) {
	desired := ptr.Deref(dep.Spec.Replicas, 1)
	s := dep.Status

	if s.ObservedGeneration < dep.Generation {
		return metav1.ConditionFalse, ReasonRolloutInProgress,
			"Waiting for the Deployment controller to observe the latest spec"
	}

	for _, c := range s.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return metav1.ConditionFalse, ReasonRolloutFailed, c.Message
		}
	}

	switch {
	case s.UpdatedReplicas < desired:
		return metav1.ConditionFalse, ReasonRolloutInProgress,
			fmt.Sprintf("%d/%d replicas updated", s.UpdatedReplicas, desired)
	case s.Replicas > s.UpdatedReplicas:
		return metav1.ConditionFalse, ReasonRolloutInProgress,
			fmt.Sprintf("%d old replicas pending termination", s.Replicas-s.UpdatedReplicas)
	case s.AvailableReplicas < s.UpdatedReplicas:
		return metav1.ConditionFalse, ReasonRolloutInProgress,
			fmt.Sprintf("%d/%d updated replicas available", s.AvailableReplicas, s.UpdatedReplicas)
	default:
		return metav1.ConditionTrue, ReasonAvailable,
			fmt.Sprintf("%d/%d replicas updated and available", s.AvailableReplicas, desired)
	}
}
