package proxmox

import (
	"context"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CommonStatusReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *CommonStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	logger.Info("Reconciling CommonStatus")

	return ctrl.Result{}, nil
}

func (r *CommonStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {

	virtualMachineHandler := handler.EnqueueRequestsFromMapFunc((func(ctx context.Context, vm client.Object) []ctrl.Request {
		//
		vmList := &proxmoxv1alpha1.VirtualMachineList{}
		if err := mgr.GetClient().List(ctx, vmList); err != nil {
			mgr.GetLogger().Error(err, "while listing VirtualMachineList")
			return nil
		}
		reqs := make([]ctrl.Request, 0, len(vmList.Items))
		for _, item := range vmList.Items {
			reqs = append(reqs, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: item.GetNamespace(),
					Name:      item.GetName(),
				},
			})
		}
		return reqs
	}))

	containerHandler := handler.EnqueueRequestsFromMapFunc((func(ctx context.Context, container client.Object) []ctrl.Request {
		//
		containerList := &proxmoxv1alpha1.ContainerList{}
		if err := mgr.GetClient().List(ctx, containerList); err != nil {
			mgr.GetLogger().Error(err, "while listing ContainerList")
			return nil
		}
		reqs := make([]ctrl.Request, 0, len(containerList.Items))
		for _, item := range containerList.Items {
			reqs = append(reqs, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: item.GetNamespace(),
					Name:      item.GetName(),
				},
			})
		}
		return reqs
	}))

	return ctrl.NewControllerManagedBy(mgr).
		Named("CommonStatusReconciler").
		Watches(&proxmoxv1alpha1.VirtualMachine{}, virtualMachineHandler).
		Watches(&proxmoxv1alpha1.Container{}, containerHandler).
		Complete(r)
}
