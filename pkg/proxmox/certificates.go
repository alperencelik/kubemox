package proxmox

import (
	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CreateCustomCertificate creates a custom certificate object in proxmox node
func CreateCustomCertificate(nodeName string, proxmoxCertSpec *proxmoxv1alpha1.ProxmoxCertSpec) error {
	// get the node object
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	certificate := proxmoxCertSpec.Certificate
	privateKey := proxmoxCertSpec.PrivateKey
	force := proxmoxCertSpec.Force
	restartProxy := proxmoxCertSpec.RestartProxy

	certSpec := &proxmox.CustomCertificate{
		Certificates: certificate,
		Force:        force,
		Key:          privateKey,
		Restart:      restartProxy,
	}
	// Create the certificate object in proxmox node
	return node.UploadCustomCertificate(ctx, certSpec)
}

// Delete certificate object from proxmox node
func DeleteCustomCertificate(nodeName string) {
	// get the node object
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Delete the certificate object from proxmox node
	log.Log.Info("Deleting the certificate from the Proxmox node", "Node", nodeName)
	err = node.DeleteCustomCertificate(ctx)
	if err != nil {
		log.Log.Error(err, "unable to delete custom certificate")
	}
}
