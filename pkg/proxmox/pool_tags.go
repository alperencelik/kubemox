package proxmox

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	optionPool = "pool"
	optionTags = "tags"

	tagTaskTimesNum = 5
	tagTaskSteps    = 10
)

// poolTagOptions are the options of a machine created from scratch: the
// resource pool and the tags. A clone gets its pool on the clone call.
func poolTagOptions(vm *proxmoxv1alpha1.VirtualMachine) []proxmox.VirtualMachineOption {
	opts := []proxmox.VirtualMachineOption{}
	if vm.Spec.Pool != "" {
		opts = append(opts, proxmox.VirtualMachineOption{Name: optionPool, Value: vm.Spec.Pool})
	}
	if tags := desiredTags(vm); len(tags) > 0 {
		opts = append(opts, proxmox.VirtualMachineOption{Name: optionTags, Value: strings.Join(tags, ";")})
	}
	return opts
}

// desiredTags are the spec's tags plus the operator's own, deduplicated and
// sorted so that the comparison with Proxmox is stable.
func desiredTags(vm *proxmoxv1alpha1.VirtualMachine) []string {
	seen := map[string]bool{virtualMachineTag: true}
	tags := []string{virtualMachineTag}
	for _, t := range vm.Spec.Tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// missingTags are the desired tags the machine does not carry yet.
func missingTags(vm *proxmoxv1alpha1.VirtualMachine, cfg *proxmox.VirtualMachineConfig) []string {
	have := map[string]bool{}
	for _, t := range strings.Split(cfg.Tags, ";") {
		if t != "" {
			have[t] = true
		}
	}
	missing := []string{}
	for _, t := range desiredTags(vm) {
		if !have[t] {
			missing = append(missing, t)
		}
	}
	return missing
}

// ConfigureTags adds the spec's tags to an existing machine. Tags that are on
// the machine but not in the spec are left alone.
func (pc *ProxmoxClient) ConfigureTags(vm *proxmoxv1alpha1.VirtualMachine) error {
	virtualMachine, err := pc.getVirtualMachine(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	cfg := virtualMachine.VirtualMachineConfig
	if cfg == nil {
		return nil
	}
	for _, tag := range missingTags(vm, cfg) {
		task, err := virtualMachine.AddTag(ctx, tag)
		if err != nil {
			if proxmox.IsErrNoop(err) {
				continue
			}
			return fmt.Errorf("adding tag %q to VirtualMachine %s: %w", tag, vm.Spec.Name, err)
		}
		ok, _, taskErr := task.WaitForCompleteStatus(ctx, tagTaskTimesNum, tagTaskSteps)
		if !ok {
			return &TaskError{ExitStatus: task.ExitStatus}
		}
		if taskErr != nil {
			return fmt.Errorf("adding tag %q to VirtualMachine %s: %w", tag, vm.Spec.Name, taskErr)
		}
	}
	return nil
}
