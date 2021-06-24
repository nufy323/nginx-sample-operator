/*
Copyright 2021.

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

package controllers

import (
	"context"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/example/nginx-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// NginxReconciler reconciles a Nginx object
type NginxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=proxy.example.com,resources=nginxes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxy.example.com,resources=nginxes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxy.example.com,resources=nginxes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Nginx object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *NginxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// get CR Nginx
	nginx := &proxyv1alpha1.Nginx{}
	err := r.Get(ctx, req.NamespacedName, nginx)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Nginx resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Nginx")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	foundDep := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: nginx.Name, Namespace: nginx.Namespace}, foundDep)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.NewDeploymentForNginx(nginx)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	expectedCount := nginx.Spec.Count
	if *foundDep.Spec.Replicas != expectedCount {
		foundDep.Spec.Replicas = &expectedCount
		err = r.Update(ctx, foundDep)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", foundDep.Namespace, "Deployment.Name", foundDep.Name)
			return ctrl.Result{}, err
		}
		// Ask to requeue after 1 minute in order to give enough time for the
		// pods be created on the cluster side and the operand be able
		// to do the next update step accurately.
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Update the nginx status with the pod names
	// List the pods for this nginx's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(nginx.Namespace),
		client.MatchingLabels(labelsForNginx()),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods", "nginx.Namespace", nginx.Namespace, "nginx.Name", nginx.Name)
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	if !reflect.DeepEqual(podNames, nginx.Status.Nodes) {
		nginx.Status.Nodes = podNames
		err := r.Status().Update(ctx, nginx)
		if err != nil {
			log.Error(err, "Failed to update nginx status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// deploymentFornginx returns a nginx Deployment object
func (r *NginxReconciler) NewDeploymentForNginx(m *proxyv1alpha1.Nginx) *appsv1.Deployment {
	replicas := m.Spec.Count
	ls := labelsForNginx()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "nginx:1.14.2",
						Name:  "nginx",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
						}},
					}},
				},
			},
		},
	}
	// Set Memcached instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

func labelsForNginx() map[string]string {
	return map[string]string{"app": "nginx"}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// SetupWithManager sets up the controller with the Manager.
func (r *NginxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.Nginx{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
