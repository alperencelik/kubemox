package proxmox

import (
	"context"
	"fmt"
	"reflect"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func classifyItems[I any](desiredItems, actualItems []I, getKey func(I) string) (
	itemsToAdd, itemsToUpdate, itemsToDelete []I) {
	// Create a map of actual items
	actualItemsMap := make(map[string]I)
	for _, item := range actualItems {
		key := getKey(item)
		actualItemsMap[key] = item
	}

	itemKeyList := make([]string, len(desiredItems))
	for i, item := range desiredItems {
		key := getKey(item)
		itemKeyList[i] = key
		if actualItem, ok := actualItemsMap[key]; ok {
			if !reflect.DeepEqual(item, actualItem) {
				itemsToUpdate = append(itemsToUpdate, item)
			}
		} else {
			itemsToAdd = append(itemsToAdd, item)
		}
	}

	for _, item := range actualItems {
		key := getKey(item)
		if !utils.StringInSlice(key, itemKeyList) {
			itemsToDelete = append(itemsToDelete, item)
		}
	}
	return
}

func applyChanges[I any](
	ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine,
	itemsToAdd, itemsToUpdate, itemsToDelete []I,
	getDeviceID func(I) string,
	addConfig func(context.Context, *proxmoxv1alpha1.VirtualMachine, I) error,
	updateConfig func(context.Context, *proxmoxv1alpha1.VirtualMachine, I) error,
	operationName string,
	// preUpdate func(I) error, // Pre-conditions before updating the item
) error {
	for _, item := range itemsToAdd {
		deviceID := getDeviceID(item)
		log.Log.Info(fmt.Sprintf("Adding %s %s to VirtualMachine %s", operationName, deviceID, vm.Name))
		if err := addConfig(ctx, vm, item); err != nil {
			return err
		} else {
			log.Log.Info(fmt.Sprintf("%s %s of VirtualMachine %s has been added", operationName, deviceID, vm.Name))
		}
	}

	for _, item := range itemsToUpdate {
		deviceID := getDeviceID(item)

		if err := updateConfig(ctx, vm, item); err != nil {
			return err
		} else {
			log.Log.Info(fmt.Sprintf("%s %s of VirtualMachine %s has been updated", operationName, deviceID, vm.Name))
		}
	}

	for _, item := range itemsToDelete {
		deviceID := getDeviceID(item)
		log.Log.Info(fmt.Sprintf("Deleting %s %s of VirtualMachine %s", operationName, deviceID, vm.Name))
		if task, err := deleteVirtualMachineOption(vm, deviceID); err != nil {
			taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
			if !taskStatus {
				// Return the task.ExitStatus
				return &TaskError{ExitStatus: task.ExitStatus}
			}
			if !taskCompleted {
				log.Log.Error(taskErr, fmt.Sprintf("Failed to delete %s %s of VirtualMachine %s", operationName, deviceID, vm.Name))
				return taskErr
			}
			return err
		} else {
			log.Log.Info(fmt.Sprintf("%s %s of VirtualMachine %s has been deleted", operationName, deviceID, vm.Name))
		}
	}
	return nil
}
