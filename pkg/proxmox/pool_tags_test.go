package proxmox

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const testTag = "web"

func poolTagOptionMap(opts []proxmox.VirtualMachineOption) map[string]string {
	m := map[string]string{}
	for _, o := range opts {
		m[o.Name] = fmt.Sprintf("%v", o.Value)
	}
	return m
}

func TestPoolTagOptions(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Spec.Pool = "tenant-a"
	vm.Spec.Tags = []string{testTag, virtualMachineTag, testTag, ""}
	got := poolTagOptionMap(poolTagOptions(vm))
	want := map[string]string{"pool": "tenant-a", "tags": "kubemox;web"}
	if len(got) != len(want) {
		t.Fatalf("options = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("option %s = %q, want %q", k, got[k], v)
		}
	}
	// Without a spec there is nothing but the operator tag.
	bare := poolTagOptionMap(poolTagOptions(&proxmoxv1alpha1.VirtualMachine{}))
	if len(bare) != 1 || bare["tags"] != virtualMachineTag {
		t.Errorf("bare options = %v", bare)
	}
}

func TestMissingTags(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Spec.Tags = []string{testTag}
	cfg := &proxmox.VirtualMachineConfig{Tags: "kubemox;other"}
	if missing := missingTags(vm, cfg); strings.Join(missing, ",") != testTag {
		t.Fatalf("missingTags = %v", missing)
	}
	cfg.Tags = "kubemox;other;web"
	if missing := missingTags(vm, cfg); len(missing) != 0 {
		t.Fatalf("no tags missing expected, got %v", missing)
	}
}
