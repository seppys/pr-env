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
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// PreviewEnvironmentReconciler reconciles a PreviewEnvironment object
type PreviewEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// PreviewDomain is the base domain preview hosts are created under: <name>.<PreviewDomain>.
	PreviewDomain string
	// IngressClassName is set on every Ingress; empty uses the cluster's default IngressClass.
	IngressClassName string
}

const (
	desiredReplicas int32  = 1
	selectorLabel   string = "app.kubernetes.io/instance"
	// httpPortName links the container port, the Service targetPort and the Ingress backend by name.
	httpPortName string = "http"
)

// +kubebuilder:rbac:groups=preview.seppys.dev,resources=previewenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=preview.seppys.dev,resources=previewenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=preview.seppys.dev,resources=previewenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;delete

func (r *PreviewEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	environment := &previewv1alpha1.PreviewEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, environment); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	labels := map[string]string{selectorLabel: environment.Name}

	ns, err := r.reconcileNamespace(ctx, environment)
	if err != nil {
		return r.failReconcile(ctx, environment, ReasonNamespaceReconcileFailed, err)
	}

	dep, err := r.reconcileDeployment(ctx, environment, labels, ns)
	if err != nil {
		return r.failReconcile(ctx, environment, ReasonDeploymentReconcileFailed, err)
	}

	if err := r.reconcileService(ctx, environment, labels, ns); err != nil {
		return r.failReconcile(ctx, environment, ReasonServiceReconcileFailed, err)
	}

	ing, err := r.reconcileIngress(ctx, environment, ns)
	if err != nil {
		return r.failReconcile(ctx, environment, ReasonIngressReconcileFailed, err)
	}

	setReady(environment, dep, ing)

	if err := r.Status().Update(ctx, environment); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PreviewEnvironmentReconciler) reconcileNamespace(
	ctx context.Context, environment *previewv1alpha1.PreviewEnvironment,
) (*corev1.Namespace, error) {
	log := logf.FromContext(ctx)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: environment.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		ns.Name = environment.Name

		return controllerutil.SetControllerReference(environment, ns, r.Scheme)
	})
	if err != nil {
		return nil, fmt.Errorf("reconciling Namespace: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Namespace", "namespace", ns.Name, "operation", op)
	}

	return ns, nil
}

func (r *PreviewEnvironmentReconciler) reconcileDeployment(
	ctx context.Context, environment *previewv1alpha1.PreviewEnvironment, labels map[string]string, ns *corev1.Namespace,
) (*appsv1.Deployment, error) {
	log := logf.FromContext(ctx)

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: environment.Name, Namespace: ns.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec.Replicas = new(desiredReplicas)

		if dep.CreationTimestamp.IsZero() {
			dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		dep.Spec.Template.Labels = labels

		if len(dep.Spec.Template.Spec.Containers) == 0 {
			dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, corev1.Container{Name: "app"})
		}
		c := &dep.Spec.Template.Spec.Containers[0]
		c.Image = environment.Spec.Image
		c.Ports = []corev1.ContainerPort{{
			Name:          httpPortName,
			ContainerPort: environment.Spec.Port,
			Protocol:      corev1.ProtocolTCP,
		}}

		return controllerutil.SetControllerReference(environment, dep, r.Scheme)
	})
	if err != nil {
		return nil, fmt.Errorf("reconciling Deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Deployment", "deployment", dep.Name, "operation", op)
	}

	return dep, nil
}

func (r *PreviewEnvironmentReconciler) reconcileService(
	ctx context.Context, environment *previewv1alpha1.PreviewEnvironment, labels map[string]string, ns *corev1.Namespace,
) error {
	log := logf.FromContext(ctx)

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: environment.Name, Namespace: ns.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       httpPortName,
			Port:       80,
			TargetPort: intstr.FromString(httpPortName),
			Protocol:   corev1.ProtocolTCP,
		}}

		return controllerutil.SetControllerReference(environment, svc, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling Service: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Service", "service", svc.Name, "operation", op)
	}

	return nil
}

func (r *PreviewEnvironmentReconciler) reconcileIngress(
	ctx context.Context, environment *previewv1alpha1.PreviewEnvironment, ns *corev1.Namespace,
) (*networkingv1.Ingress, error) {
	log := logf.FromContext(ctx)

	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: environment.Name, Namespace: ns.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		// Left untouched when unset, so the API server's default IngressClass isn't overwritten.
		if r.IngressClassName != "" {
			ing.Spec.IngressClassName = new(r.IngressClassName)
		}
		ing.Spec.Rules = []networkingv1.IngressRule{{
			Host: r.previewHost(environment),
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path:     "/",
					PathType: new(networkingv1.PathTypePrefix),
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: environment.Name,
						Port: networkingv1.ServiceBackendPort{Name: httpPortName},
					}},
				}},
			}},
		}}

		return controllerutil.SetControllerReference(environment, ing, r.Scheme)
	})
	if err != nil {
		return nil, fmt.Errorf("reconciling Ingress: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Ingress", "ingress", ing.Name, "operation", op)
	}

	return ing, nil
}

// previewHost is the hostname the preview is served on: <prNumber>-<name>.<PreviewDomain>.
// The first label must be a valid DNS label (at most 63 characters); otherwise the API server
// rejects the Ingress.
func (r *PreviewEnvironmentReconciler) previewHost(environment *previewv1alpha1.PreviewEnvironment) string {
	return fmt.Sprintf("%d-%s.%s", environment.Spec.PRNumber, environment.Name, r.PreviewDomain)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreviewEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&previewv1alpha1.PreviewEnvironment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Named("previewenvironment").
		Complete(r)
}
