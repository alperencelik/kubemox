package framework

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pollInterval = 5 * time.Second
)

// WaitForCondition polls until the given object has the specified condition status.
func (f *Framework) WaitForCondition(
	ctx context.Context,
	obj client.Object,
	condType string,
	status metav1.ConditionStatus,
	timeout time.Duration,
) error {
	key := client.ObjectKeyFromObject(obj)

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if err := f.KubeClient.Get(ctx, key, obj); err != nil {
			return false, nil //nolint:nilerr // retry on transient errors
		}

		condAccessor, err := meta.Accessor(obj)
		if err != nil {
			return false, err
		}

		// Use the status subresource conditions — extract from the runtime object
		condObj, ok := obj.(interface {
			GetConditions() []metav1.Condition
		})
		if !ok {
			return false, fmt.Errorf("object %s does not implement GetConditions", condAccessor.GetName())
		}

		for _, c := range condObj.GetConditions() {
			if c.Type == condType && c.Status == status {
				return true, nil
			}
		}
		return false, nil
	})
}

// CreateAndWait creates the given object and waits for a Ready condition.
func (f *Framework) CreateAndWait(ctx context.Context, obj client.Object, timeout time.Duration) error {
	if err := f.KubeClient.Create(ctx, obj); err != nil {
		return fmt.Errorf("creating %s: %w", obj.GetName(), err)
	}
	return f.WaitForCondition(ctx, obj, "Ready", metav1.ConditionTrue, timeout)
}

// DeleteAndWaitGone deletes the object and waits until it no longer exists.
func (f *Framework) DeleteAndWaitGone(ctx context.Context, obj client.Object, timeout time.Duration) error {
	if err := client.IgnoreNotFound(f.KubeClient.Delete(ctx, obj)); err != nil {
		return fmt.Errorf("deleting %s: %w", obj.GetName(), err)
	}

	key := client.ObjectKeyFromObject(obj)
	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		err := f.KubeClient.Get(ctx, key, obj)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				return true, nil
			}
			return false, nil //nolint:nilerr // retry on transient errors
		}
		return false, nil
	})
}

// WaitForOperatorReady waits until the operator Deployment has Available condition.
func (f *Framework) WaitForOperatorReady(ctx context.Context, timeout time.Duration) error {
	deploy := &appsv1.Deployment{}
	key := client.ObjectKey{
		Namespace: f.Config.Operator.Namespace,
		Name:      f.Config.Operator.DeploymentName,
	}

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if err := f.KubeClient.Get(ctx, key, deploy); err != nil {
			return false, nil //nolint:nilerr // retry
		}
		for _, c := range deploy.Status.Conditions {
			if c.Type == appsv1.DeploymentAvailable && c.Status == "True" {
				return true, nil
			}
		}
		return false, nil
	})
}
