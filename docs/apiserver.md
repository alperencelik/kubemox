# Aggregated apiserver — request flow

The kubemox aggregated apiserver serves the `ops.proxmox.alperen.cloud` API
group. Every resource it exposes is a live projection of Proxmox state plus
imperative verbs (start/stop/reboot/shutdown/metrics); nothing is
stored in etcd. See `api/ops/v1alpha1/doc.go` for the resource map and
kubectl usage examples; this document explains what happens *behind* those
calls.

## Layers

```
        WIRE              kube-apiserver
          │                     │
          │   APIService aggregation routing
          ▼                     ▼
   ┌─────────────────────────────────────────────┐
   │ kubemox-apiserver                           │
   │                                             │
   │   pkg/apiserver/server  (HTTP/TLS, options) │
   │           │                                 │
   │           ▼                                 │
   │   pkg/apiserver/registry  (dispatch table)  │
   │           │                                 │
   │           ▼                                 │
   │   PowerStorage.Create  (per-verb handler)   │
   │       │                                     │
   │       ├──► proxmoxconn.Resolver             │
   │       │       └──► controller-runtime cache ──► etcd (kubemox CR)
   │       │                                     │
   │       └──► proxmox.ProxmoxClient.PowerVMAsync
   │                   └──► go-proxmox HTTP ───────────────► Proxmox REST API
   │                                             │
   └─────────────────────────────────────────────┘
```

| Package | Responsibility |
|---|---|
| `pkg/apiserver/server` | HTTP/TLS, options, scheme + codecs, glue from cobra to `genericapiserver` |
| `pkg/apiserver/registry` | Dispatch table mapping resource paths to `rest.Storage` impls |
| `pkg/apiserver/proxmoxconn` | Resolver: kubemox CR name → (Proxmox client, node, VM name) |
| `pkg/proxmox` (ops.go) | Context-aware Proxmox helpers used by the apiserver |
| `api/ops/v1alpha1` | Wire types served by the apiserver |

## Power-off, traced end-to-end

A `kubectl create --raw .../virtualmachines/new-test-0/stop` with a
`PowerAction` body. Each numbered step corresponds to a real function call
in the codebase; line numbers point at the entry point.

```
kubectl                                                                                 Proxmox
  │                                                                                       │
  │ POST /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/new-test-0/stop         │
  │ {"apiVersion":"ops.../v1alpha1","kind":"PowerAction","forceStop":true}                │
  ▼                                                                                       │
┌────────────────────────┐                                                                │
│ kube-apiserver         │  sees group=ops.proxmox.alperen.cloud → looks up APIService    │
│ (in the cluster)       │  → proxies the raw HTTP to the aggregated apiserver Service    │
└──────────┬─────────────┘                                                                │
           │                                                                              │
           ▼                                                                              │
┌─────────────────────────────────────────────────────────────────────┐                   │
│ kubemox-apiserver pod  (cmd/apiserver/main.go → server.Options.Run) │                   │
│                                                                     │                   │
│  1. genericapiserver routes path → looks up storage for             │                   │
│     "virtualmachines/stop"  (registry/install.go)                   │                   │
│         = PowerStorage{verb: PowerVerbStop}                         │                   │
│                                                                     │                   │
│  2. Codecs decode body → *pveopsv1alpha1.PowerAction                │                   │
│     (via Scheme registered in server/scheme.go)                     │                   │
│                                                                     │                   │
│  3. PowerStorage implements rest.NamedCreater → dispatcher calls:   │                   │
│                                                                     │                   │
│     PowerStorage.Create(ctx, "new-test-0", *PowerAction, ...)       │                   │
│         │   (registry/virtualmachine/power.go:62)                   │                   │
│         │                                                           │                   │
│         ├──► resolver.ResolveVM(ctx, "new-test-0")                  │                   │
│         │       │   (proxmoxconn/resolver.go:52)                    │                   │
│         │       │                                                   │                   │
│         │       ├──► r.Client.Get(ctx, "new-test-0") ──┐            │                   │
│         │       │       (controller-runtime cache)     │  watch     │                   │
│         │       │                                      │  cache     │                   │
│         │       │   ┌──────────────────────────────────▼──┐         │                   │
│         │       │   │ kubemox VirtualMachine CR (etcd)    │         │                   │
│         │       │   │   .Spec.ConnectionRef.Name = "prod" │         │                   │
│         │       │   │   .Spec.NodeName          = "lowtower"        │                   │
│         │       │   └─────────────────────────────────────┘         │                   │
│         │       │                                                   │                   │
│         │       └──► proxmox.NewProxmoxClientFromRef(ctx, ..., "prod")                  │
│         │             ▲                                             │                   │
│         │             │ (cached per ProxmoxConnection resourceVersion)                  │
│         │             │                                             │                   │
│         │           returns *proxmox.ProxmoxClient                  │                   │
│         │                                                           │                   │
│         │       returns VMTarget{CR, Client, NodeName, VMName}      │                   │
│         │                                                           │                   │
│         ├──► s.verb.toProxmox()  =  proxmox.PowerStop               │                   │
│         │                                                           │                   │
│         └──► target.Client.PowerVMAsync(ctx, "new-test-0", "lowtower", PowerStop)       │
│                 │   (pkg/proxmox/ops.go)                            │                   │
│                 │                                                   │                   │
│                 ├──► getVMByNameCtx(ctx, "new-test-0", "lowtower")  │                   │
│                 │       │   (pkg/proxmox/ops.go)                    │                   │
│                 │       │                                           │                   │
│                 │       ├──► pc.getNode(ctx, "lowtower") ──────────────►  GET /nodes    │
│                 │       │                                           │                   │
│                 │       ├──► cache check: VM ID?                    │                   │
│                 │       │     │ miss                                │                   │
│                 │       │     ▼                                     │                   │
│                 │       └──► node.VirtualMachines(ctx) ─────────────────►  GET /nodes/  │
│                 │              walks response, finds vm,            │       lowtower/   │
│                 │              caches ID + handle                   │       qemu        │
│                 │                                                   │                   │
│                 ├──► switch verb { case PowerStop: vm.Stop(ctx) } ──────► POST /nodes/  │
│                 │                                                   │     lowtower/     │
│                 │                                                   │     qemu/105/     │
│                 │                                                   │     status/stop   │
│                 │                                                   │                   │
│                 │      Proxmox returns immediately with a task UPID │                   │
│                 │      (it does NOT wait for the stop to finish)    │                   │
│                 │                                                   │                   │
│                 └──► returns "UPID:lowtower:0001A2B3:..."           │                   │
│                                                                     │                   │
│  4. Build response:                                                 │                   │
│     out := action.DeepCopy()                                        │                   │
│     out.TaskRef = &TaskRef{UPID: "...", Node: "lowtower"}           │                   │
│                                                                     │                   │
│  5. Codecs encode *PowerAction → JSON                               │                   │
└──────────┬──────────────────────────────────────────────────────────┘                   │
           │                                                                              │
           ▼                                                                              │
┌────────────────────────┐                                                                │
│ kube-apiserver         │  streams response back                                         │
└──────────┬─────────────┘                                                                │
           ▼                                                                              │
kubectl receives:
{ "apiVersion":"ops.proxmox.alperen.cloud/v1alpha1","kind":"PowerAction",
  "forceStop":true,
  "taskRef":{"upid":"UPID:lowtower:...","node":"lowtower"} }
```

## Things worth noticing

**Two backing stores, not one.** Every request reads from etcd (via the
watch-backed controller-runtime cache, to find *which* Proxmox + *which*
node hosts the VM) *and* hits Proxmox (to actually do the work). The
Resolver is the bridge — it never talks to Proxmox itself, only translates
"VM name" → "client + node + name."

**The async/sync line.** `PowerVMAsync` deliberately does not wait for the
Proxmox task to finish. It fires the command, gets back a UPID, returns.
The client polls `/apis/ops.proxmox.alperen.cloud/v1alpha1/tasks/{upid}`
to learn when the stop actually finishes. This keeps apiserver requests
short and predictable; the alternative (block until VM is stopped) could
keep an HTTP connection open for minutes.

**Same shape for every verb.** Start, stop, reboot, shutdown all flow
through the same `PowerStorage.Create` — the only difference is the
`PowerVerb` constant baked in at registration time in `registry/install.go`.

| Subresource | Storage | Proxmox-side call |
|---|---|---|
| `virtualmachines/start` | `PowerStorage` (PowerVerbStart) | `vm.Start(ctx)` |
| `virtualmachines/stop` | `PowerStorage` (PowerVerbStop) | `vm.Stop(ctx)` |
| `virtualmachines/reboot` | `PowerStorage` (PowerVerbReboot) | `vm.Reboot(ctx)` |
| `virtualmachines/shutdown` | `PowerStorage` (PowerVerbShutdown) | `vm.Shutdown(ctx)` |
| `virtualmachines/metrics` | `MetricsStorage` (GET) | `vm.Ping(ctx)` |

The resolver step is identical for all of them; once you understand the
power-off trace above, you understand every verb.

**List vs. Get for `virtualmachines`.** `Get` of a single VM hits Proxmox
live (`target.Client.UpdateVMStatus(...)`). `List` does *not* fan out to
Proxmox per VM — it reflects whatever the kubemox controller last wrote
into `cr.Status.Status`. So list output may be slightly stale; if you need
authoritative live state, do a `Get` by name. This is deliberate so that
`kubectl get vm.ops...` does not melt the Proxmox API under load.
