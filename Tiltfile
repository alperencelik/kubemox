
docker_build('kubemox', '.', dockerfile='Dockerfile')
yaml = helm(
    'charts/kubemox',
  # The release name, equivalent to helm --name
  name='kubemox',
  # The namespace to install in, equivalent to helm --namespace
  namespace='default',
  # The values file to substitute into the chart.
  values=['charts/kubemox/values.yaml'],
  # Values to set from the command-line
  set=['proxmox.endpoint=10.0.0.99', 'proxmox.insecureSkipTLSVerify=true', 'proxmox.username=root@pam', 'proxmox.password=$PASS', 'image.repository=kubemox']
)

k8s_yaml(yaml)
k8s_resource('kubemox', port_forwards=8000)