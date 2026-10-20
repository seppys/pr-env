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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// These are plain unit tests: setReadyFromDeployment only reads the objects it is given,
// so no API server is involved.
var _ = Describe("setReadyFromDeployment", func() {
	// rollout describes the Deployment fields setReadyFromDeployment looks at.
	type rollout struct {
		generation         int64
		observedGeneration int64
		replicas           int32 // total pods, old and new
		updated            int32
		available          int32
		deadlineExceeded   bool
	}

	newDeployment := func(r rollout) *appsv1.Deployment {
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Generation: r.generation},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: r.observedGeneration,
				Replicas:           r.replicas,
				UpdatedReplicas:    r.updated,
				AvailableReplicas:  r.available,
			},
		}
		if r.deadlineExceeded {
			dep.Status.Conditions = []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentProgressing,
				Status:  corev1.ConditionFalse,
				Reason:  "ProgressDeadlineExceeded",
				Message: `ReplicaSet "app-123" has timed out progressing.`,
			}}
		}
		return dep
	}

	newEnvironment := func() *previewv1alpha1.PreviewEnvironment {
		return &previewv1alpha1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
	}

	DescribeTable("sets the Ready condition from the rollout state",
		func(r rollout, wantStatus metav1.ConditionStatus, wantReason string) {
			env := newEnvironment()
			setReadyFromDeployment(env, newDeployment(r))

			cond := meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(wantStatus))
			Expect(cond.Reason).To(Equal(wantReason))
			Expect(cond.Message).NotTo(BeEmpty())
			Expect(cond.ObservedGeneration).To(Equal(env.Generation))
		},
		Entry("not Ready while the Deployment controller hasn't seen the latest spec",
			rollout{generation: 2, observedGeneration: 1, replicas: 1, updated: 1, available: 1},
			metav1.ConditionFalse, ReasonRolloutInProgress),
		Entry("not Ready while the old pod still serves and no replica is updated yet",
			rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 0, available: 1},
			metav1.ConditionFalse, ReasonRolloutInProgress),
		Entry("not Ready while old replicas are pending termination",
			rollout{generation: 1, observedGeneration: 1, replicas: 2, updated: 1, available: 1},
			metav1.ConditionFalse, ReasonRolloutInProgress),
		Entry("not Ready while the updated replica isn't available yet",
			rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 1, available: 0},
			metav1.ConditionFalse, ReasonRolloutInProgress),
		Entry("RolloutFailed when the progress deadline is exceeded",
			rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 0, available: 1, deadlineExceeded: true},
			metav1.ConditionFalse, ReasonRolloutFailed),
		Entry("Ready once every replica is updated and available",
			rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 1, available: 1},
			metav1.ConditionTrue, ReasonAvailable),
	)

	Describe("LastTransitionTime", func() {
		ready := rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 1, available: 1}
		inProgress := rollout{generation: 1, observedGeneration: 1, replicas: 1, updated: 0, available: 1}
		past := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

		// Pins the condition's LastTransitionTime to a known past value so the
		// assertions don't depend on how much time passes between calls.
		readyWithPastTransition := func() *previewv1alpha1.PreviewEnvironment {
			env := newEnvironment()
			setReadyFromDeployment(env, newDeployment(ready))
			meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady).LastTransitionTime = past
			return env
		}

		It("is kept when the status doesn't change, so repeated reconciles don't rewrite status", func() {
			env := readyWithPastTransition()
			setReadyFromDeployment(env, newDeployment(ready))

			cond := meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady)
			Expect(cond.LastTransitionTime).To(Equal(past))
		})

		It("is updated when the status flips", func() {
			env := readyWithPastTransition()
			setReadyFromDeployment(env, newDeployment(inProgress))

			cond := meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.LastTransitionTime.After(past.Time)).To(BeTrue())
		})
	})
})
