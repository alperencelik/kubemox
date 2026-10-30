/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxmox

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

var _ = Describe("Container template validation", func() {
	ctx := context.Background()
	const (
		vztmpl = "local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst"
		tpl    = "tpl"
	)
	rootfs := []proxmoxv1alpha1.ContainerTemplateDisk{{Storage: "local-lvm", Size: 8}}
	keys := []string{"ssh-ed25519 AAAA user@host"}
	vlan := func(v int32) *int32 { return &v }

	// Run against the CRD from config/crd/bases, so the real CEL rules decide.
	DescribeTable("the CRD admits one template source and create-only fields only on create",
		func(name string, template proxmoxv1alpha1.ContainerTemplate, admitted bool) {
			ct := &proxmoxv1alpha1.Container{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: proxmoxv1alpha1.ContainerSpec{
					Name: name, NodeName: "pve1", Template: template,
					ConnectionRef: &corev1.LocalObjectReference{Name: "conn"},
				},
			}
			err := k8sClient.Create(ctx, ct)
			if admitted {
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, ct)).To(Succeed())
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		},
		Entry("clone", "ct-clone", proxmoxv1alpha1.ContainerTemplate{Name: tpl}, true),
		Entry("osTemplate with a rootfs and guest settings", "ct-ostemplate", proxmoxv1alpha1.ContainerTemplate{
			OSTemplate: vztmpl, Disk: rootfs, Unprivileged: true, SSHPublicKeys: keys,
			Network: []proxmoxv1alpha1.ContainerTemplateNetwork{{Bridge: "vmbr1", VLAN: vlan(201), IP: "198.18.1.23/24"}},
		}, true),
		Entry("osTemplate without a rootfs", "x-ostemplate-nodisk",
			proxmoxv1alpha1.ContainerTemplate{OSTemplate: vztmpl}, false),
		Entry("clone and osTemplate together", "x-clone-and-ostemplate",
			proxmoxv1alpha1.ContainerTemplate{Name: tpl, OSTemplate: vztmpl, Disk: rootfs}, false),
		Entry("no source", "x-no-source", proxmoxv1alpha1.ContainerTemplate{Disk: rootfs}, false),
		Entry("clone with SSH keys", "x-clone-keys",
			proxmoxv1alpha1.ContainerTemplate{Name: tpl, SSHPublicKeys: keys}, false),
		Entry("clone made unprivileged", "x-clone-unprivileged",
			proxmoxv1alpha1.ContainerTemplate{Name: tpl, Unprivileged: true}, false),
		Entry("VLAN out of range", "x-vlan", proxmoxv1alpha1.ContainerTemplate{
			Name: tpl, Network: []proxmoxv1alpha1.ContainerTemplateNetwork{{VLAN: vlan(4095)}},
		}, false),
		Entry("address without prefix length", "x-ip", proxmoxv1alpha1.ContainerTemplate{
			Name: tpl, Network: []proxmoxv1alpha1.ContainerTemplateNetwork{{IP: "198.18.1.23"}},
		}, false),
	)
})
