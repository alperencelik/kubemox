package proxmox

import (
	"context"
	"fmt"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ExternalWatchers struct {
	mu       sync.Mutex
	Watchers map[string]chan struct{}
}

var (
	ObserveInterval = 20 * time.Second
)

type Resource interface {
	client.Object
}

type FetchResourceFunc func(ctx context.Context, key client.ObjectKey, obj Resource) error
type UpdateStatusFunc func(ctx context.Context, obj Resource) error
type CheckDeltaFunc func(obj Resource) (bool, error)
type HandleAutoStartFunc func(ctx context.Context, obj Resource) (ctrl.Result, error)
type HandleReconcileFunc func(ctx context.Context, obj Resource) (ctrl.Result, error)
type DeleteWatcherFunc func(name string)

func NewExternalWatchers() *ExternalWatchers {
	return &ExternalWatchers{
		Watchers: make(map[string]chan struct{}),
	}
}

func (e *ExternalWatchers) HandleWatcher(ctx context.Context, req ctrl.Request,
	startWatcherFunc func(ctx context.Context, stopChan chan struct{}) (ctrl.Result, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if the watcher exists
	if _, exists := e.Watchers[req.Name]; !exists {
		stopChan := make(chan struct{})
		e.Watchers[req.Name] = stopChan
		resultChan := make(chan ctrl.Result)
		errChan := make(chan error)
		go func() {
			var result ctrl.Result
			var err error
			result, err = startWatcherFunc(ctx, stopChan)
			if err != nil {
				errChan <- err
				return
			}
			resultChan <- result
		}()
	}
}

func StartWatcher(ctx context.Context, resource Resource,
	stopChan chan struct{}, fetchResource FetchResourceFunc, updateStatus UpdateStatusFunc,
	checkDelta CheckDeltaFunc, handleAutoStart HandleAutoStartFunc, handleReconcile HandleReconcileFunc,
	deleteWatcher DeleteWatcherFunc) (ctrl.Result, error) {
	ticker := time.NewTicker(ObserveInterval)
	defer ticker.Stop()
	logger := log.FromContext(ctx)
	resourceName := resource.GetName()

	for {
		select {
		case <-ticker.C:
			result, err := handleAutoStart(ctx, resource)
			if err != nil {
				logger.Error(err, "Error handling auto start")
			}
			if result.Requeue {
				logger.Info(fmt.Sprintf("Requeueing resource %s", resourceName))
				// TODO: Re-evaluate the requirement of deleting the watcher
				deleteWatcher(resourceName)
				return ctrl.Result{Requeue: true}, nil
			}

			err = fetchResource(ctx, client.ObjectKey{Namespace: resource.GetNamespace(), Name: resourceName}, resource)
			if err != nil {
				logger.Error(err, "Error getting resource")
				return ctrl.Result{}, err
			}

			err = updateStatus(ctx, resource)
			if err != nil {
				logger.Error(err, "Error updating resource status")
				return ctrl.Result{}, err
			}

			triggerReconcile, err := checkDelta(resource)
			if err != nil {
				logger.Error(err, "Error comparing resource state")
				return ctrl.Result{}, err
			}
			if triggerReconcile {
				logger.Info(fmt.Sprintf("Triggering the reconciliation of resource due to configuration drift %s", resourceName))
				_, err = handleReconcile(ctx, resource)
				if err != nil {
					logger.Error(err, "Error while triggering the reconciliation of resource")
					return ctrl.Result{}, err
				}
			}

		case <-stopChan:
			logger.Info(fmt.Sprintf("Watcher for resource %s is stopped", resourceName))
			return ctrl.Result{}, nil
		}
	}
}

func (e *ExternalWatchers) DeleteWatcher(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if stopChan, exists := e.Watchers[name]; exists {
		close(stopChan)
		delete(e.Watchers, name)
	}
}
