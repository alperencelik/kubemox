package proxmox

import (
	"context"
	"fmt"
	"time"

	"github.com/alperencelik/kubemox/pkg/guestname"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// nameConflictRequeue is how often a resource that lost its spec.name to an
// older one checks whether the name has been freed.
const nameConflictRequeue = 30 * time.Second

type conditionedObject interface {
	client.Object
	GetConditions() []metav1.Condition
	SetConditions([]metav1.Condition)
}

// claimGuest reports whether obj owns the Proxmox guest named by its claim. It
// must run before a reconcile touches Proxmox. When obj doesn't own it, obj is
// marked NameConflict and requeued, or, if it is being deleted, its finalizer
// is dropped without deleting the guest; the caller returns res, err.
func claimGuest(ctx context.Context, c client.Client, obj conditionedObject, claim guestname.Claim,
	finalizer string) (owned bool, res ctrl.Result, err error) {
	owner, err := guestname.Owner(ctx, c, claim)
	if err != nil {
		return false, ctrl.Result{}, err
	}

	if owner.SameObject(claim) {
		if meta.FindStatusCondition(obj.GetConditions(), guestname.ConditionNameConflict) == nil {
			return true, ctrl.Result{}, nil
		}
		patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
		conds := obj.GetConditions()
		meta.RemoveStatusCondition(&conds, guestname.ConditionNameConflict)
		obj.SetConditions(conds)
		return true, ctrl.Result{}, client.IgnoreNotFound(c.Status().Patch(ctx, obj, patch))
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		if controllerutil.RemoveFinalizer(obj, finalizer) {
			log.FromContext(ctx).Info("Releasing finalizer without deleting the guest, it belongs to "+owner.String(),
				"guest", claim.Guest)
			return false, ctrl.Result{}, client.IgnoreNotFound(c.Update(ctx, obj))
		}
		return false, ctrl.Result{}, nil
	}

	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	conds := obj.GetConditions()
	changed := meta.SetStatusCondition(&conds, metav1.Condition{
		Type:   guestname.ConditionNameConflict,
		Status: metav1.ConditionTrue,
		Reason: "NameTaken",
		Message: fmt.Sprintf("spec.name %q on node %q is managed by %s, which claimed it first",
			claim.Guest, claim.Node, owner),
		ObservedGeneration: obj.GetGeneration(),
	})
	if changed {
		obj.SetConditions(conds)
		if err := c.Status().Patch(ctx, obj, patch); err != nil {
			return false, ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	return false, ctrl.Result{RequeueAfter: nameConflictRequeue}, nil
}
