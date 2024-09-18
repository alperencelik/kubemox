package proxmox

import (
	"fmt"
	"reflect"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func CheckVirtualMachineTemplateDelta(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := getVirtualMachine(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
	}
	virtualMachineConfig := VirtualMachine.VirtualMachineConfig
	// Compare with the desired VM
	actualMachineSpec := VirtualMachineComparison{
		Cores:   virtualMachineConfig.Cores,
		Sockets: virtualMachineConfig.Sockets,
		Memory:  int(virtualMachineConfig.Memory),
	}
	desiredMachineSpec := VirtualMachineComparison{
		Cores:   vmTemplate.Spec.VirtualMachineConfig.Cores,
		Sockets: vmTemplate.Spec.VirtualMachineConfig.Sockets,
		Memory:  vmTemplate.Spec.VirtualMachineConfig.Memory,
	}
	cloudInitDiff, err := CheckVirtualMachineTemplateCIConfig(vmTemplate)
	if err != nil {
		log.Log.Error(err, "Error checking cloud-init config")
	}
	// Compare the actual VM with the desired VM with spec and cloud-init config
	if !reflect.DeepEqual(actualMachineSpec, desiredMachineSpec) || cloudInitDiff {
		fmt.Printf("Desired: %v\n", desiredMachineSpec)
		fmt.Printf("Actual: %v\n", actualMachineSpec)
		fmt.Printf("Cloud-init diff: %v\n", cloudInitDiff)
		return true, nil
	}
	return false, nil
}

func ConvertVMToTemplate(vmName, nodeName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for converting to template")
	}
	// If the VM is not a template, convert it to a template
	if !VirtualMachine.Template {
		task, err := VirtualMachine.ConvertToTemplate(ctx)
		if err != nil {
			return err
		}
		_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
		if !taskCompleted {
			log.Log.Error(taskErr, "Can't convert VM to template")
		} else {
			log.Log.Info(fmt.Sprintf("VirtualMachine %s has been converted to template", vmName))
		}
	}
	return nil
}

func SetCloudInitConfig(vmName, nodeName string, ciConfig *proxmoxv1alpha1.CloudInitConfig) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating cloud-init config")
	}
	// Get cloud-init configuration and compare with the desired state
	actualCloudInitConfig, err := GetCloudInitConfig(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting cloud-init config")
	}

	// Compare with the desired VM
	if !cmp.Equal(*ciConfig, actualCloudInitConfig, cloudInitcompareOptions()...) {
		log.Log.Info(fmt.Sprintf("Cloud-init config is updating with new values for VM %s", vmName))
		CloudInitOptions := constructCloudInitOptions(ciConfig)
		// Empty the current cloud-init config
		err := EmptyCloudInitConfig(vmName, nodeName)
		if err != nil {
			log.Log.Error(err, "Error emptying cloud-init config")
		}
		// Update cloud-init config
		task, err := VirtualMachine.Config(ctx, CloudInitOptions...)
		if err != nil {
			return err
		}
		// Wait for task completion
		_, firstTaskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
		if taskErr != nil {
			log.Log.Error(taskErr, "Can't update cloud-init config")
		}

		// Regenerate the cloud-init image
		task, err = VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
			Name:  "ide2",
			Value: "local-lvm:cloudinit",
		})
		if err != nil {
			return err
		}
		_, secondTaskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
		if taskErr != nil {
			log.Log.Error(taskErr, "Can't update cloud-init config")
		}
		taskCompleted := firstTaskCompleted && secondTaskCompleted

		switch taskCompleted {
		case false:
			log.Log.Error(taskErr, "Can't update cloud-init config")
		case true:
			log.Log.Info(fmt.Sprintf("Cloud-init config has been updated for VM %s", vmName))
		default:
			log.Log.Info("Cloud-init config is already updated")
		}
	}
	return nil
}

func ImportDiskToVM(vmName, nodeName, diskName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for importing disk")
	}
	// Check if scsi0 is already imported or not
	scsi0 := VirtualMachine.VirtualMachineConfig.SCSI0
	if scsi0 != "" {
		return nil
	}
	// TODO: Get disk location from cluster storage
	diskLocation := "/var/lib/vz/template/iso/" + diskName
	log.Log.Info(fmt.Sprintf("Disk location: %s", diskLocation))

	// Import disk
	// TODO: SCSI0 and local-lvm:0 should be retrieved from external resource
	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "scsi0",
		Value: "local-lvm:0,import-from=" + diskLocation,
	})
	if err != nil {
		panic(err)
	}

	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 10, 10)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't import disk to VM")
	case true:
		log.Log.Info(fmt.Sprintf("Disk %s has been imported to VM %s", diskName, vmName))
	default:
		log.Log.Info("Disk is already imported")
	}

	return nil
}

func AddCloudInitDrive(vmName, nodeName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for adding cloud-init drive")
	}
	// Check if cloud-init drive is already added or not
	cloudInitDrive := VirtualMachine.VirtualMachineConfig.IDE2
	if cloudInitDrive != "" {
		return nil
	}

	// Add cloud-init drive
	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "ide2",
		Value: "local-lvm:cloudinit",
	})
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 10, 10)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't add cloud-init drive to VM")
	case true:
		log.Log.Info(fmt.Sprintf("Cloud-init drive has been added to VM %s", vmName))
	default:
		log.Log.Info("Cloud-init drive is already added")
	}
	return nil
}

func SetBootOrder(vmName, nodeName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for setting boot order")
	}
	// Set boot order
	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name: "boot",
		// TODO: SCSI0 should be retrieved from external resource
		Value: "order=scsi0",
	})
	if err != nil {
		return err
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't set boot order for VM")
	}
	return nil
}

func IsVMTemplate(vmName, nodeName string) bool {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for checking if it's a template")
	}
	if VirtualMachine.Template {
		return true
	} else {
		return false
	}
}

func CreateVMTemplate(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (*proxmox.Task, error) {
	vmTemplateName := vmTemplate.Spec.Name
	nodeName := vmTemplate.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	vmID, err := getNextVMID(Client)
	if err != nil {
		log.Log.Error(err, "Error getting next VMID")
	}
	virtualMachineSpec := vmTemplate.Spec.VirtualMachineConfig
	networkConfig := fmt.Sprintf("%s,bridge=%s", virtualMachineSpec.Network.Model, virtualMachineSpec.Network.Bridge)

	VMOptions := []proxmox.VirtualMachineOption{
		{
			Name:  virtualMachineSocketOption,
			Value: virtualMachineSpec.Sockets,
		},
		{
			Name:  virtualMachineCPUOption,
			Value: virtualMachineSpec.Cores,
		},
		{
			Name:  virtualMachineMemoryOption,
			Value: virtualMachineSpec.Memory,
		},
		{
			Name:  "name",
			Value: vmTemplateName,
		},
		{
			Name:  "net0",
			Value: networkConfig,
		},
		{
			Name:  "scsihw",
			Value: "virtio-scsi-pci",
		},
	}

	// Create VM
	task, err := node.NewVirtualMachine(ctx, vmID, VMOptions...)
	if err != nil {
		panic(err)
	}
	return task, err
}

func UpdateVirtualMachineTemplate(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	VirtualMachine, err := getVirtualMachine(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
	}
	// Update VirtualMachineTemplate with the desired state
	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  virtualMachineCPUOption,
		Value: vmTemplate.Spec.VirtualMachineConfig.Cores,
	}, proxmox.VirtualMachineOption{
		Name:  virtualMachineSocketOption,
		Value: vmTemplate.Spec.VirtualMachineConfig.Sockets,
	}, proxmox.VirtualMachineOption{
		Name:  virtualMachineMemoryOption,
		Value: vmTemplate.Spec.VirtualMachineConfig.Memory,
	})
	if err != nil {
		return err
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't update VM")
	}
	// Update cloud-init config
	if reconfigureCloudInit, _ := CheckVirtualMachineTemplateCIConfig(vmTemplate); reconfigureCloudInit {
		err := SetCloudInitConfig(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName, &vmTemplate.Spec.CloudInitConfig)
		if err != nil {
			log.Log.Error(err, "Error updating cloud-init config")
		}
	}

	return nil
}

func CheckVirtualMachineTemplateCIConfig(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (bool, error) {
	desiredCloudInitConfig := vmTemplate.Spec.CloudInitConfig
	actualCloudInitConfig, err := GetCloudInitConfig(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting cloud-init config")
	}
	// Compare with the desired cloud-init config
	if !cmp.Equal(desiredCloudInitConfig, actualCloudInitConfig, cloudInitcompareOptions()...) {
		return true, nil
	}
	return false, nil
}

func constructCloudInitOptions(cloudInitConfig *proxmoxv1alpha1.CloudInitConfig) []proxmox.VirtualMachineOption {
	// Reflection to get the non-empty fields and their corresponding cloud-init options
	v := reflect.ValueOf(*cloudInitConfig)
	t := reflect.TypeOf(*cloudInitConfig)

	var CloudInitOptions []proxmox.VirtualMachineOption

	// Map of struct field names to their corresponding cloud-init option names
	fieldToOptionName := map[string]string{
		"User":            "ciuser",
		"Password":        "cipassword",
		"DNSDomain":       "searchdomain",
		"DNSServers":      "nameserver",
		"SSHKeys":         "sshkeys",
		"UpgradePackages": "ciupgrade",
		"IPConfig":        "ipconfig0",
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldName := t.Field(i).Name
		optionName, ok := fieldToOptionName[fieldName]

		if !ok {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			if field.String() != "" {
				CloudInitOptions = append(CloudInitOptions, proxmox.VirtualMachineOption{
					Name:  optionName,
					Value: field.String(),
				})
			}
		case reflect.Slice:
			if field.Len() > 0 {
				CloudInitOptions = append(CloudInitOptions, proxmox.VirtualMachineOption{
					Name:  optionName,
					Value: field.Interface(),
				})
			}
		case reflect.Bool:
			if field.Bool() {
				CloudInitOptions = append(CloudInitOptions, proxmox.VirtualMachineOption{
					Name:  optionName,
					Value: field.Bool(),
				})
			}
		}
	}
	return CloudInitOptions
}

func GetCloudInitConfig(vmName, nodeName string) (proxmoxv1alpha1.CloudInitConfig, error) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for getting cloud-init config")
	}
	virtualMachineConfig := VirtualMachine.VirtualMachineConfig
	cloudInitConfig := &proxmoxv1alpha1.CloudInitConfig{
		User:            virtualMachineConfig.CIUser,
		Password:        "", // Password is not returned as plain text
		DNSDomain:       virtualMachineConfig.Searchdomain,
		DNSServers:      strings.Split(virtualMachineConfig.Nameserver, ""),
		SSHKeys:         strings.Split(virtualMachineConfig.SSHKeys, ""),
		UpgradePackages: virtualMachineConfig.CIUpgrade != 0,
	}
	return *cloudInitConfig, nil
}

func EmptyCloudInitConfig(vmName, nodeName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for emptying cloud-init config")
	}
	// Empty the current cloud-init config
	EmptyCloudInitOptions := []proxmox.VirtualMachineOption{
		{
			Name:  "ciuser",
			Value: "",
		},
		{
			Name:  "cipassword",
			Value: "",
		},
		{
			Name:  "searchdomain",
			Value: "",
		},
		{
			Name:  "nameserver",
			Value: "",
		},
		{
			Name:  "sshkeys",
			Value: "",
		},
		{
			Name:  "ciupgrade",
			Value: false,
		},
		{
			Name:  "ipconfig0",
			Value: "",
		},
	}
	task, err := VirtualMachine.Config(ctx, EmptyCloudInitOptions...)
	if err != nil {
		return err
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't empty cloud-init config")
	case true:
		// "Cloud-init config has been emptied for VM %s", vmName
	default:
		log.Log.Info("Cloud-init config is already empty")
	}

	return nil
}

func cloudInitcompareOptions() []cmp.Option {
	domainTransformer := cmp.Transformer("TrimSpace", strings.TrimSpace)

	serversTransformer := cmp.Transformer("NormalizeDNSServers", func(s []string) []string {
		var normalized []string
		for _, str := range s {
			trimmed := strings.TrimSpace(str)
			if trimmed != "" {
				normalized = append(normalized, trimmed)
			}
		}
		if len(normalized) == 0 {
			return nil
		}
		return normalized
	})
	return []cmp.Option{
		domainTransformer,
		serversTransformer,
		cmpopts.IgnoreFields(proxmoxv1alpha1.CloudInitConfig{}, "Password"),
	}
}
