# Contributing

You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster. The project is using [Kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) to generate the controller and CRDs. For Proxmox interaction the project is using [go-proxmox](https://github.com/luthermonson/go-proxmox) project. The controllers are located under `internal/controllers/proxmox` directory and the external packages `proxmox` and `kubernetes` are located under `pkg` directory.

- To create a new controller you can use the following command:

```bash
kubebuilder create api --group proxmox --version v1alpha1 --kind NewKind 
```

- Define the spec and status of your new kind in `api/proxmox/v1alpha1/newkind_types.go` file.

- Define the controller logic in `internal/controllers/proxmox/newkind_controller.go` file.