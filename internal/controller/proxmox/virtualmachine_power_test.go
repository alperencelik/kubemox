package proxmox

import (
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestWantsStart(t *testing.T) {
	cases := []struct {
		power     proxmoxv1alpha1.PowerState
		autoStart bool
		want      bool
	}{
		{"", true, true},
		{"", false, false},
		{proxmoxv1alpha1.PowerStateRunning, false, true},
		{proxmoxv1alpha1.PowerStateStopped, true, false},
		{proxmoxv1alpha1.PowerStateManual, true, false},
	}
	for _, tc := range cases {
		vm := &proxmoxv1alpha1.VirtualMachine{}
		vm.Spec.PowerState = tc.power
		vm.Spec.EnableAutoStart = tc.autoStart
		if got := wantsStart(vm); got != tc.want {
			t.Errorf("wantsStart(power=%q, autoStart=%v) = %v, want %v", tc.power, tc.autoStart, got, tc.want)
		}
	}
}
