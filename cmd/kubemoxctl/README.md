# kubemoxctl

Small helper for moving kubemox custom resources between clusters — typical flow: local `kind` bootstrap cluster to the real cluster running on the VMs kubemox just provisioned.

Inspired by `clusterctl move`: one command pauses the source, copies every kubemox CR to the target, and resumes the target. No Proxmox VMs are re-created — the controllers adopt the existing VMs via `CheckVM(name, node)`.

## Commands

- `move` — one-shot: pause source, copy, resume target. **Use this for the migration.**
- `export` / `import` / `resume` — the underlying steps if you want to drive them by hand or keep a YAML archive.

## Build

```sh
go build -o bin/kubemoxctl ./cmd/kubemoxctl
```

## Test workflow — one-shot `move`

Source is the current kubeconfig/context (same as every kubectl-style tool). Point at the target with `--to-kubeconfig` — the two clusters don't need to share a kubeconfig.

```sh
./bin/kubemoxctl move \
  --to-kubeconfig /path/to/target-kubeconfig.yaml \
  --archive       /tmp/kubemox-backup.yaml
```

Same kubeconfig, different contexts? Use `--to-context`:

```sh
./bin/kubemoxctl move --to-context talos
```

What happens, in order:

1. Stamps `proxmox.alperen.cloud/reconcile-mode: Disable` on every kubemox CR on the source.
2. Lists + scrubs them, optionally writes `--archive` as a backup.
3. Creates them on the target in topological order (ProxmoxConnection first, then templates, then VMs, then children). Each resource is created with the Disable annotation set.
4. Rewrites ownerReferences UIDs as each parent gets created.
5. Clears the Disable annotation on the target.
6. Leaves the source paused. Delete the source CRs manually once you've verified the target.

### Flags

- `--kubeconfig <path>` / `--context <name>` — source overrides. Optional; defaults to `KUBECONFIG` / `~/.kube/config` / current-context.
- `--to-kubeconfig <path>` — target kubeconfig path (clusterctl-style).
- `--to-context <name>` — target context. Works with `--to-kubeconfig`, or alone to pick a context from the source kubeconfig.
- `--archive <file>` — optional backup archive written mid-move.
- `--pause-source=false` — skip the source pause (not recommended).
- `--resume-target=false` — leave the target paused at the end (use when you want to eyeball things before controllers start reconciling).

## Test workflow — manual steps

```sh
# 1. Pause source and dump to file.
./bin/kubemoxctl export --context kind-kubemox --pause-source -o /tmp/kubemox.yaml

# 2. Eyeball the archive.
less /tmp/kubemox.yaml

# 3. Replay on the target (paused by default).
./bin/kubemoxctl import --context talos -i /tmp/kubemox.yaml

# 4. Sanity-check.
kubectl --context talos get virtualmachines,proxmoxconnections -A

# 5. Unpause.
./bin/kubemoxctl resume --context talos
```

## What gets moved

Ten kinds, in this order (parents first):

1. ProxmoxConnection (cluster-scoped)
2. VirtualMachineTemplate
3. StorageDownloadURL
4. VirtualMachineSet
5. VirtualMachine (cluster-scoped)
6. ManagedVirtualMachine (cluster-scoped)
7. VirtualMachineSnapshot
8. VirtualMachineSnapshotPolicy
9. Container
10. CustomCertificate

Credentials live inline in `ProxmoxConnection.spec`, so no Secret handling is needed.

## What the tool does not do

- Apply CRDs on the target. Install the Helm chart (or `make install`) there first.
- Copy the kubemox operator Deployment. That's the chart's job.
- Delete anything on the source after the move. Cleanup is manual and deliberate — the source is left paused so you can verify before removing.
- Update resources that already exist on the target. `Create` fails loudly on collisions; re-runs need a clean target or manual cleanup.
