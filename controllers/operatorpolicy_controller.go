// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"
)

const (
	OperatorControllerName string = "operator-policy-controller"
)

var OpLog = ctrl.Log.WithName(OperatorControllerName)

// OperatorPolicyReconciler reconciles a OperatorPolicy object
type OperatorPolicyReconciler struct {
	client.Client
	DynamicWatcher depclient.DynamicWatcher
}

// SetupWithManager sets up the controller with the Manager and will reconcile when the dynamic watcher
// sees that an object is updated
func (r *OperatorPolicyReconciler) SetupWithManager(mgr ctrl.Manager, depEvents *source.Channel) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(OperatorControllerName).
		For(&policyv1beta1.OperatorPolicy{}).
		Watches(depEvents, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

// blank assignment to verify that OperatorPolicyReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &OperatorPolicyReconciler{}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// (user): Modify the Reconcile function to compare the state specified by
// the OperatorPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *OperatorPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	policy := &policyv1beta1.OperatorPolicy{}

	watcher := depclient.ObjectIdentifier{
		Group:     operatorv1.GroupVersion.Group,
		Version:   operatorv1.GroupVersion.Version,
		Kind:      "OperatorPolicy",
		Namespace: req.Namespace,
		Name:      req.Name,
	}

	// get the operator policy on the cluster
	err := r.Get(ctx, req.NamespacedName, policy)
	if err != nil {
		if errors.IsNotFound(err) {
			OpLog.Info("Operator policy could not be found")

			err := r.DynamicWatcher.RemoveWatcher(watcher)
			if err != nil {
				OpLog.Error(err, "Error updating dependency watcher. Ignoring the failure.")
			}

			return reconcile.Result{}, nil
		}

		OpLog.Error(err, "Failed to get operator policy")

		return reconcile.Result{}, err
	}

	// handle the policy
	OpLog.Info("Reconciling OperatorPolicy", "policy", policy.Name)

	err = r.DynamicWatcher.StartQueryBatch(watcher)
	if err != nil {
		panic(err)
	}

	// check for subscription
	subscriptionSpec := new(operatorv1alpha1.Subscription)
	err = r.Get(context.TODO(),
		types.NamespacedName{Namespace: policy.Spec.Subscription.Namespace, Name: policy.Spec.Subscription.SubscriptionSpec.Package},
		subscriptionSpec)
	exists := err == nil || !errors.IsNotFound(err)
	shouldExist := strings.EqualFold(string(policy.Spec.ComplianceType), string(policyv1.MustHave))

	// Subscription does not exist but it should exist
	if !exists && shouldExist {
		if strings.EqualFold(string(policy.Spec.RemediationAction), string(policyv1.Enforce)) {
			OpLog.Info("creating kind " + subscriptionSpec.Kind + " in ns " + subscriptionSpec.Namespace)
			subscriptionSpec := buildSubscription(policy, subscriptionSpec)
			err = r.Create(context.TODO(), subscriptionSpec)

			if err != nil {
				r.setCompliance(policy, policyv1.NonCompliant)
				OpLog.Error(err, "Could not handle missing musthave object")

				return reconcile.Result{}, err
			}

			csvChan := make(chan *operatorv1alpha1.ClusterServiceVersionList)

			go func() {
				defer close(csvChan)
				csvData := &operatorv1alpha1.ClusterServiceVersionList{}
				err = r.busyWaitForCSV(30*time.Second, csvData, subscriptionSpec)

				csvChan <- csvData
			}()

			// Currently creates an OperatorGroup for every Subscription
			// in the same ns, and defaults to targeting all ns.
			// Future implementations will enable targeting ns based on
			// installModes supported by the CSV. Also, only one OperatorGroup
			// should exist in each ns

			// if policy contains no operatorgroup spec, check if there is allnamespaces
			// if not, create one

			if policy.Spec.OperatorGroup != nil {
				operatorGroup := buildOperatorGroup(policy)
				if operatorGroup != nil {
					err = r.Create(context.TODO(), operatorGroup)
				}
			} else {
				ogSpec := new(operatorv1.OperatorGroup)
				err := r.Get(context.TODO(),
					types.NamespacedName{Namespace: subscriptionSpec.Namespace, Name: "all-ns-og"}, ogSpec)
				exists = !errors.IsNotFound(err)

				if !exists {
					gvk := schema.GroupVersionKind{
						Group:   "operators.coreos.com",
						Version: "v1",
						Kind:    "OperatorGroup",
					}

					ogSpec.SetGroupVersionKind(gvk)
					ogSpec.ObjectMeta.SetName("all-ns-og")
					ogSpec.ObjectMeta.SetNamespace(subscriptionSpec.Namespace)

					err = r.Create(context.TODO(), ogSpec)
				}
			}

			if err != nil {
				r.setCompliance(policy, policyv1.NonCompliant)
				OpLog.Error(err, "Could not handle missing musthave object")

				return reconcile.Result{}, err
			}

			csvs := <-csvChan

			// list of csvs
			for i := range csvs.Items {
				deployment := csvs.Items[i]
				_ = deployment
			}

			r.setCompliance(policy, policyv1.Compliant)

			return reconcile.Result{}, nil
		}
	}

	err = r.DynamicWatcher.EndQueryBatch(watcher)
	if err != nil {
		panic(err)
	}

	err = r.handleSinglePolicy(policy)
	if err != nil {
		OpLog.Error(err, "Error while evaluating operator policy")

		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *OperatorPolicyReconciler) getOperatorPolicyDependencies(
	policy *policyv1beta1.OperatorPolicy,
) error {
	return nil
}

// handleSinglePolicy encapsulates the logic for processing a single operatorPolicy.
// Currently, the controller is able to create an OLM Subscription from the operatorPolicy specs,
// create an OperatorGroup in the ns as the Subscription, set the compliance status, and log the policy.
//
// In the future, more reconciliation logic will be added. For reference:
// https://github.com/JustinKuli/ocm-enhancements/blob/89-operator-policy/enhancements/sig-policy/89-operator-policy-kind/README.md
func (r *OperatorPolicyReconciler) handleSinglePolicy(
	policy *policyv1beta1.OperatorPolicy,
) error {
	OpLog.Info("Handling OperatorPolicy", "policy", policy.Name)

	subscriptionSpec := new(operatorv1alpha1.Subscription)
	err := r.Get(context.TODO(),
		types.NamespacedName{Namespace: policy.Spec.Subscription.Namespace, Name: policy.Spec.Subscription.SubscriptionSpec.Package},
		subscriptionSpec)
	exists := err == nil || !errors.IsNotFound(err)
	shouldExist := strings.EqualFold(string(policy.Spec.ComplianceType), string(policyv1.MustHave))

	// Object does not exist but it should exist, create object
	// Create new watches on subscription, CSV/Deployments(?), InstallPlan, operatorgroup(?), catalogsources
	// Does CSV contain info regarding updates?
	// Most likely, just need to watch the deployments that is created for the operator installation
	// Should include all resources that contributes to the status field of the operator policy (check docs)
	if !exists && shouldExist {
		if strings.EqualFold(string(policy.Spec.RemediationAction), string(policyv1.Enforce)) {
			OpLog.Info("creating kind " + subscriptionSpec.Kind + " in ns " + subscriptionSpec.Namespace)
			subscriptionSpec := buildSubscription(policy, subscriptionSpec)
			err = r.Create(context.TODO(), subscriptionSpec)

			if err != nil {
				r.setCompliance(policy, policyv1.NonCompliant)
				OpLog.Error(err, "Could not handle missing musthave object")

				return err
			}

			// Currently creates an OperatorGroup for every Subscription
			// in the same ns, and defaults to targeting all ns.
			// Future implementations will enable targeting ns based on
			// installModes supported by the CSV. Also, only one OperatorGroup
			// should exist in each ns

			// if policy contains no operatorgroup spec, check if there is allnamespaces
			// if not, create one

			if policy.Spec.OperatorGroup != nil {
				operatorGroup := buildOperatorGroup(policy)
				if operatorGroup != nil {
					err = r.Create(context.TODO(), operatorGroup)
				}
			} else {
				ogSpec := new(operatorv1.OperatorGroup)
				err := r.Get(context.TODO(),
					types.NamespacedName{Namespace: subscriptionSpec.Namespace, Name: "all-ns-og"}, ogSpec)
				exists = !errors.IsNotFound(err)

				if !exists {
					gvk := schema.GroupVersionKind{
						Group:   "operators.coreos.com",
						Version: "v1",
						Kind:    "OperatorGroup",
					}

					ogSpec.SetGroupVersionKind(gvk)
					ogSpec.ObjectMeta.SetName("all-ns-og")
					ogSpec.ObjectMeta.SetNamespace(subscriptionSpec.Namespace)

					err = r.Create(context.TODO(), ogSpec)
				}
			}

			if err != nil {
				r.setCompliance(policy, policyv1.NonCompliant)
				OpLog.Error(err, "Could not handle missing musthave object")

				return err
			}

			csvChan := make(chan *operatorv1alpha1.ClusterServiceVersionList)

			go func() {
				defer close(csvChan)
				csvData := &operatorv1alpha1.ClusterServiceVersionList{}
				err = r.busyWaitForCSV(20*time.Second, csvData, subscriptionSpec)

				csvChan <- csvData
			}()

			csvs := <-csvChan

			// list of csvs
			// Retrieve installed CSV name
			// csvName = r.Get(context.TODO(),
			// 	types.NamespacedName{Name: policy.Spec.Subscription.Package, Namespace: policy.Spec.Subscription.Namespace},
			// 	subscriptionSpec)

			// Create watcher object
			watcher := depclient.ObjectIdentifier{
				Group:     "policy.open-cluster-management.io",
				Version:   "v1beta1",
				Kind:      "OperatorPolicy",
				Name:      policy.Name,
				Namespace: policy.Namespace,
			}

			// Add watches
			err = r.DynamicWatcher.StartQueryBatch(watcher)
			if err != nil {
				panic(err)
			}

			gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
			err = r.Get(context.TODO(),
				types.NamespacedName{Namespace: policy.Spec.Subscription.Namespace, Name: policy.Spec.Subscription.SubscriptionSpec.Package},
				subscriptionSpec)
			for i := range csvs.Items {
				deployment := csvs.Items[i]
				if deployment.Name == subscriptionSpec.Status.InstalledCSV {
					_, err := r.DynamicWatcher.Get(watcher, gvk, deployment.Namespace, deployment.Name)
					if err != nil {
						panic(err)
					}
				}
			}

			err = r.DynamicWatcher.EndQueryBatch(watcher)
			if err != nil {
				panic(err)
			}

			r.setCompliance(policy, policyv1.Compliant)

			return nil
		}

		// Inform
		r.setCompliance(policy, policyv1.NonCompliant)

		return nil
	}

	// Object exists but it should not exist, delete object
	// Deleting related objects will be added in the future
	if exists && !shouldExist {
		if strings.EqualFold(string(policy.Spec.RemediationAction), string(policyv1.Enforce)) {
			OpLog.Info("deleting kind " + subscriptionSpec.Kind + " in ns " + subscriptionSpec.Namespace)
			err = r.Delete(context.TODO(), subscriptionSpec)

			if err != nil {
				r.setCompliance(policy, policyv1.NonCompliant)
				OpLog.Error(err, "Could not handle existing musthave object")

				return err
			}

			r.setCompliance(policy, policyv1.Compliant)

			return nil
		}

		// Inform
		r.setCompliance(policy, policyv1.NonCompliant)

		return nil
	}

	// Object does not exist and it should not exist, emit success event
	if !exists && !shouldExist {
		OpLog.Info("The object does not exist and is compliant with the mustnothave compliance type")
		// Future implementation: Possibly emit a success event

		return nil
	}

	// Object exists, now need to validate field to make sure they match
	if exists {
		OpLog.Info("The object already exists. Checking fields to verify matching specs")
		// Future implementation: Verify the specs of the object matches the one on the cluster

		return nil
	}

	return nil
}

// updatePolicyStatus updates the status of the operatorPolicy.
//
// In the future, a condition should be added as well, and this should generate events.
func (r *OperatorPolicyReconciler) updatePolicyStatus(
	policy *policyv1beta1.OperatorPolicy,
) error {
	updatedStatus := policy.Status

	err := r.Get(context.TODO(), types.NamespacedName{Namespace: policy.Namespace, Name: policy.Name}, policy)
	if err != nil {
		OpLog.Info(fmt.Sprintf("Failed to refresh policy; using previously fetched version: %s", err))
	} else {
		policy.Status = updatedStatus
	}

	err = r.Status().Update(context.TODO(), policy)
	if err != nil {
		OpLog.Info(fmt.Sprintf("Failed to update policy status: %s", err))

		return err
	}

	return nil
}

// shouldEvaluatePolicy will determine if the policy is ready for evaluation by checking
// for the compliance status. If it is already compliant, then evaluation will be skipped.
// It will be evaluated otherwise.
//
// In the future, other mechanisms for determining evaluation should be considered.
func (r *OperatorPolicyReconciler) shouldEvaluatePolicy(
	policy *policyv1beta1.OperatorPolicy,
) bool {
	if policy.Status.ComplianceState == policyv1.Compliant {
		OpLog.Info(fmt.Sprintf("%s is already compliant, skipping evaluation", policy.Name))

		return false
	}

	return true
}

func (r *OperatorPolicyReconciler) busyWaitForCSV(
	timeout time.Duration,
	csv *operatorv1alpha1.ClusterServiceVersionList,
	subscription *operatorv1alpha1.Subscription,
) error {
	csvSpecs := operatorv1alpha1.ClusterServiceVersionList{}
	subscriptionSpec := new(operatorv1alpha1.Subscription)
	start := time.Now()

	for {
		err := r.Get(context.TODO(),
			types.NamespacedName{Namespace: subscription.Namespace, Name: subscription.Name},
			subscriptionSpec)
		if err != nil {
			return err
		}
		err = r.List(context.TODO(), &csvSpecs)
		if err == nil {
			*csv = csvSpecs
		}
		elapsed := time.Since(start)
		if elapsed >= timeout {
			return err
		}
	}
}

// buildSubscription bootstraps the subscription spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildSubscription(
	policy *policyv1beta1.OperatorPolicy,
	subscription *operatorv1alpha1.Subscription,
) *operatorv1alpha1.Subscription {
	gvk := schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "Subscription",
	}

	subscription.SetGroupVersionKind(gvk)
	subscription.ObjectMeta.Name = policy.Spec.Subscription.Package
	subscription.ObjectMeta.Namespace = policy.Spec.Subscription.Namespace
	subscription.Spec = policy.Spec.Subscription.SubscriptionSpec.DeepCopy()

	return subscription
}

// Sets the compliance of the policy
func (r *OperatorPolicyReconciler) setCompliance(
	policy *policyv1beta1.OperatorPolicy,
	compliance policyv1.ComplianceState,
) {
	policy.Status.ComplianceState = compliance

	err := r.updatePolicyStatus(policy)
	if err != nil {
		OpLog.Error(err, "error while updating policy status")
	}
}

// buildOperatorGroup bootstraps the OperatorGroup spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildOperatorGroup(
	policy *policyv1beta1.OperatorPolicy,
) *operatorv1.OperatorGroup {

	if policy.Spec.OperatorGroup == nil {
		return nil
	}

	operatorGroup := new(operatorv1.OperatorGroup)

	gvk := schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1",
		Kind:    "OperatorGroup",
	}

	operatorGroup.SetGroupVersionKind(gvk)
	operatorGroup.ObjectMeta.SetName(policy.Spec.OperatorGroup.Name)
	operatorGroup.ObjectMeta.SetNamespace(policy.Spec.OperatorGroup.Namespace)
	operatorGroup.Spec.TargetNamespaces = policy.Spec.OperatorGroup.Target.Namespace

	return operatorGroup
}
