# CustomCertificate

`CustomCertificate` is a custom resource definition (CRD) that allows you to manage custom certificates in your cluster. This CRD is generally used to create a custom certificate for Proxmox VE. It utilizes the `Certificate` resource from the `cert-manager` project and uploads the certificate to Proxmox VE.

!!! warning
    The `cert-manager` should be installed in your cluster to use `CustomCertificate` resource. You can find the installation guide [here](https://cert-manager.io/docs/installation/kubernetes/).

## Creating CustomCertificate

To create a new CustomCertificate in Proxmox, you need to create a `CustomCertificate` object.

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: CustomCertificate
metadata:
  name: customcertificate-sample
spec:
  nodeName: "lowtower"
  certManagerSpec:
    commonName: "proxmox.alperen.cloud"
    dnsNames:
      - "proxmox.alperen.cloud"
    issuerRef:
      group: cert-manager.io
      kind: ClusterIssuer
      name: acme-issuer
    secretName: proxmox-alperen-cloud-tls
    usages: 
      - client auth
      - server auth
  proxmoxCertSpec: 
    nodeName: "lowtower"
    force: true
    restartProxy: true
```
