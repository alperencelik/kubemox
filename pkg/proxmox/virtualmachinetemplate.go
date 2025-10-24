package proxmox

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func (pc *ProxmoxClient) CheckVirtualMachineTemplateDelta(
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := pc.getVirtualMachine(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
		return false, err
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
	cloudInitDiff, err := pc.CheckVirtualMachineTemplateCIConfig(vmTemplate)
	if err != nil {
		log.Log.Error(err, "Error checking cloud-init config")
	}
	// Compare the actual VM with the desired VM with spec and cloud-init config
	if !reflect.DeepEqual(actualMachineSpec, desiredMachineSpec) || cloudInitDiff {
		return true, nil
	}
	return false, nil
}

func (pc *ProxmoxClient) ConvertVMToTemplate(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
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

func (pc *ProxmoxClient) SetCloudInitConfig(vmName, nodeName string, ciConfig *proxmoxv1alpha1.CloudInitConfig) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating cloud-init config")
	}
	// Get cloud-init configuration and compare with the desired state
	actualCloudInitConfig, err := pc.GetCloudInitConfig(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting cloud-init config")
	}

	// Compare with the desired VM
	if !cmp.Equal(*ciConfig, actualCloudInitConfig, cloudInitcompareOptions(ciConfig)...) {
		log.Log.Info(fmt.Sprintf("Cloud-init config is updating with new values for VM %s", vmName))
		CloudInitOptions := constructCloudInitOptions(ciConfig)
		// Empty the current cloud-init config
		err := pc.EmptyCloudInitConfig(vmName, nodeName)
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

func (pc *ProxmoxClient) ImportDiskToVM(vmName, nodeName, diskName, storageName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for importing disk")
		return err
	}
	// Check if scsi0 is already imported or not
	scsi0 := VirtualMachine.VirtualMachineConfig.SCSI0
	if scsi0 != "" {
		return nil
	}
	localStorage, err := pc.GetStorage("local")
	if err != nil {
		log.Log.Error(err, "Error getting local storage")
		return err
	}
	diskLocation := localStorage.Path + "/template/iso/" + diskName

	// Import disk
	// TODO: SCSI0  should be retrieved from external resource
	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "scsi0",
		Value: fmt.Sprintf("%s:0,import-from=%s", storageName, diskLocation),
	})
	if err != nil {
		return err
	}

	taskStatus, taskCompleted, taskWaitErr := task.WaitForCompleteStatus(ctx, 10, 10)
	if !taskStatus {
		// Return the task.ExitStatus as an error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskWaitErr
	}
	return nil
}

func (pc *ProxmoxClient) AddCloudInitDrive(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
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
		log.Log.Error(err, "Error adding cloud-init drive to VM")
		return err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 10, 10)

	if !taskStatus {
		// Return the task.ExitStatus as an error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskErr
	}
	return nil
}

func (pc *ProxmoxClient) SetBootOrder(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
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

func (pc *ProxmoxClient) IsVMTemplate(vmName, nodeName string) bool {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for checking if it's a template")
	}
	if VirtualMachine.Template {
		return true
	} else {
		return false
	}
}

func (pc *ProxmoxClient) CreateVMTemplate(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (*proxmox.Task, error) {
	vmTemplateName := vmTemplate.Spec.Name
	nodeName := vmTemplate.Spec.NodeName
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting node for creating VM template")
		return nil, err
	}
	vmID, err := getNextVMID(pc.Client)
	if err != nil {
		log.Log.Error(err, "Error getting next VMID")
		return nil, err
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
		log.Log.Error(err, "Error creating VM template")
		return nil, err
	}
	return task, err
}

func (pc *ProxmoxClient) AddTagToVMTemplate(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (*proxmox.Task, error) {
	virtualMachine, err := pc.getVirtualMachine(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for adding tag")
		return nil, err
	}
	// Add tag to VM
	task, err := virtualMachine.AddTag(ctx, virtualMachineTemplateTag)
	if err != nil {
		log.Log.Error(err, "Error adding tag to VM")
		return nil, err
	}
	return task, err
}

func (pc *ProxmoxClient) UpdateVirtualMachineTemplate(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	VirtualMachine, err := pc.getVirtualMachine(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
		return err
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
	if reconfigureCloudInit, _ := pc.CheckVirtualMachineTemplateCIConfig(vmTemplate); reconfigureCloudInit {
		err := pc.SetCloudInitConfig(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName, &vmTemplate.Spec.CloudInitConfig)
		if err != nil {
			log.Log.Error(err, "Error updating cloud-init config")
		}
	}

	return nil
}

func (pc *ProxmoxClient) CheckVirtualMachineTemplateCIConfig(vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (
	bool, error) {
	desiredCloudInitConfig := vmTemplate.Spec.CloudInitConfig
	actualCloudInitConfig, err := pc.GetCloudInitConfig(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting cloud-init config")
	}
	// Compare with the desired cloud-init config
	if !cmp.Equal(desiredCloudInitConfig, actualCloudInitConfig,
		cloudInitcompareOptions(&vmTemplate.Spec.CloudInitConfig)...) {
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
		"DNSDomain":       "searchdomain",
		"DNSServers":      "nameserver",
		"SSHKeys":         "sshkeys",
		"UpgradePackages": "ciupgrade",
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

	if cloudInitConfig.IPConfig != nil {
		ipConfig := cloudInitConfig.IPConfig
		gw := ipConfig.Gateway
		ip := ipConfig.IP
		cidr := ipConfig.CIDR
		CloudInitOptions = append(CloudInitOptions, proxmox.VirtualMachineOption{
			Name:  "ipconfig0",
			Value: fmt.Sprintf("gw=%s,ip=%s/%s", gw, ip, cidr),
		})
	}

	// Handle password separately
	Password, err := GetPasswordValue(cloudInitConfig)
	if err != nil {
		log.Log.Error(err, "Error getting password value")
	}
	if Password != "" {
		CloudInitOptions = append(CloudInitOptions, proxmox.VirtualMachineOption{
			Name:  "cipassword",
			Value: Password,
		})
	}
	// Handle custom cloud-init config
	if cloudInitConfig.Custom != nil {
		customConfig := customCloudInitConfig(cloudInitConfig.Custom)
		if customConfig.Value != "" {
			CloudInitOptions = append(CloudInitOptions, customConfig)
		}
	}

	return CloudInitOptions
}

func (pc *ProxmoxClient) GetCloudInitConfig(vmName, nodeName string) (proxmoxv1alpha1.CloudInitConfig, error) {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for getting cloud-init config")
	}
	virtualMachineConfig := VirtualMachine.VirtualMachineConfig
	cloudInitConfig := &proxmoxv1alpha1.CloudInitConfig{
		User: virtualMachineConfig.CIUser,
		// Password is not returned as plain text
		DNSDomain:       virtualMachineConfig.Searchdomain,
		DNSServers:      strings.Split(virtualMachineConfig.Nameserver, ","),
		SSHKeys:         strings.Split(virtualMachineConfig.SSHKeys, ""),
		UpgradePackages: virtualMachineConfig.CIUpgrade != 0,
		IPConfig:        IPConfigParser(virtualMachineConfig.IPConfig0),
	}
	return *cloudInitConfig, nil
}

func (pc *ProxmoxClient) EmptyCloudInitConfig(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
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
		{
			Name:  "cicustom",
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

func cloudInitcompareOptions(cloudInitconfig *proxmoxv1alpha1.CloudInitConfig) []cmp.Option {
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
	var cmpOptions []cmp.Option

	cmpOptions = append(cmpOptions, domainTransformer, serversTransformer,
		cmpopts.IgnoreFields(proxmoxv1alpha1.CloudInitConfig{}, "Password"),
		cmpopts.IgnoreFields(proxmoxv1alpha1.CloudInitConfig{}, "PasswordFrom"),
		// TODO: Implement custom cloud-init config comparison as well
		cmpopts.IgnoreFields(proxmoxv1alpha1.CloudInitConfig{}, "Custom"),
	)

	if cloudInitconfig.IPConfig == nil {
		cmpOptions = append(cmpOptions, cmpopts.IgnoreFields(proxmoxv1alpha1.CloudInitConfig{}, "IPConfig"))
	}
	return cmpOptions
}

func IPConfigParser(ipConfigStr string) *proxmoxv1alpha1.IPConfig {
	var ipConfig proxmoxv1alpha1.IPConfig

	keyValuePairs := strings.Split(ipConfigStr, ",")
	for _, pair := range keyValuePairs {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		value := kv[1]
		switch key {
		case "ip":
			if strings.Contains(value, "/") {
				parts := strings.SplitN(value, "/", 2)
				ipConfig.IP = parts[0]
				ipConfig.CIDR = parts[1]
			} else {
				ipConfig.IP = value
			}
		case "gw":
			ipConfig.Gateway = value
		case "ip6":
			if strings.Contains(value, "/") {
				parts := strings.SplitN(value, "/", 2)
				ipConfig.IPv6 = parts[0]
			} else {
				ipConfig.IPv6 = value
			}
		case "gw6":
			ipConfig.GatewayIPv6 = value
		}
	}
	return &ipConfig
}

func GetPasswordValue(config *proxmoxv1alpha1.CloudInitConfig) (string, error) {
	switch {
	case config.Password != nil && config.PasswordFrom != nil:
		return "", fmt.Errorf("both password and passwordFrom are set")
	case config.Password != nil:
		// Use the password directly
		return *config.Password, nil
	case config.PasswordFrom != nil:
		// Fetch the password from the Secret
		secretData, err := kubernetes.GetSecretData(os.Getenv("POD_NAMESPACE"), config.PasswordFrom)
		if err != nil {
			return "", err
		}
		return secretData, nil
	default:
		return "", nil
	}
}

func customCloudInitConfig(cicustom *proxmoxv1alpha1.CiCustom) proxmox.VirtualMachineOption {
	// Return an empty option if cicustom is nil
	if cicustom == nil {
		return proxmox.VirtualMachineOption{}
	}
	var parts []string

	if cicustom.UserData != "" {
		parts = append(parts, fmt.Sprintf("user=local:snippets/%s", cicustom.UserData))
	}
	if cicustom.MetaData != "" {
		parts = append(parts, fmt.Sprintf("meta=local:snippets/%s", cicustom.MetaData))
	}
	if cicustom.NetworkData != "" {
		parts = append(parts, fmt.Sprintf("network=local:snippets/%s", cicustom.NetworkData))
	}
	if cicustom.VendorData != "" {
		parts = append(parts, fmt.Sprintf("vendor=local:snippets/%s", cicustom.VendorData))
	}
	customConfig := strings.Join(parts, ",")
	// Remove \n characters
	customConfig = strings.ReplaceAll(customConfig, "\n", "")

	return proxmox.VirtualMachineOption{
		Name:  "cicustom",
		Value: customConfig,
	}
}
