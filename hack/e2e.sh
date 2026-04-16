#!/usr/bin/env bash
# Convenience script for running the full e2e test lifecycle.
# Usage: ./hack/e2e.sh
#
# Set SKIP_TEARDOWN=1 to keep the cluster and proxmox container after the run
# (useful for debugging failures). The caller is then responsible for cleanup.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

ARTIFACTS_DIR="${ARTIFACTS_DIR:-${ROOT_DIR}/_artifacts}"
export DEV_HELM_VALUES="${DEV_HELM_VALUES:-hack/e2e-values.yaml}"

collect_logs() {
    echo "=== Collecting diagnostic logs to ${ARTIFACTS_DIR} ==="
    mkdir -p "${ARTIFACTS_DIR}"
    # Best-effort collection — each command is independent and non-fatal.
    kind export logs "${ARTIFACTS_DIR}/kind-logs" --name kubemox-dev 2>/dev/null || true
    kubectl cluster-info dump --all-namespaces \
        --output-directory "${ARTIFACTS_DIR}/cluster-dump" >/dev/null 2>&1 || true
    kubectl get all -A -o wide > "${ARTIFACTS_DIR}/all-resources.txt" 2>/dev/null || true
    kubectl get events -A --sort-by=.lastTimestamp > "${ARTIFACTS_DIR}/events.txt" 2>/dev/null || true
    kubectl describe pods -l app.kubernetes.io/name=kubemox \
        > "${ARTIFACTS_DIR}/operator-describe.txt" 2>/dev/null || true
    kubectl logs -l app.kubernetes.io/name=kubemox --tail=2000 --all-containers=true \
        > "${ARTIFACTS_DIR}/operator-logs.txt" 2>/dev/null || true
    kubectl logs -l app.kubernetes.io/name=kubemox --tail=2000 --all-containers=true --previous \
        > "${ARTIFACTS_DIR}/operator-logs-previous.txt" 2>/dev/null || true
    # Kubemox CRs — dump everything to capture state at failure time.
    : > "${ARTIFACTS_DIR}/crs.yaml"
    for crd in virtualmachines managedvirtualmachines containers virtualmachinesets \
        virtualmachinesnapshots virtualmachinesnapshotpolicies virtualmachinetemplates \
        customcertificates storagedownloadurls proxmoxconnections; do
        kubectl get "${crd}.proxmox.alperen.cloud" -A -o yaml \
            >> "${ARTIFACTS_DIR}/crs.yaml" 2>/dev/null || true
    done
    # Proxmox container state.
    docker logs proxmox-dev > "${ARTIFACTS_DIR}/proxmox.log" 2>&1 || true
    docker inspect proxmox-dev > "${ARTIFACTS_DIR}/proxmox-inspect.json" 2>&1 || true
    docker ps -a > "${ARTIFACTS_DIR}/docker-ps.txt" 2>/dev/null || true
}

cleanup() {
    local exit_code=$?
    # On failure, collect diagnostics BEFORE tearing down so the kind cluster
    # and proxmox container are still alive and kubectl/docker can reach them.
    if [ "${exit_code}" -ne 0 ]; then
        collect_logs
    fi
    if [ "${SKIP_TEARDOWN:-0}" = "1" ]; then
        echo "=== Skipping teardown (SKIP_TEARDOWN=1) ==="
        return
    fi
    echo "=== Tearing down dev environment ==="
    make dev-teardown || true
}
trap cleanup EXIT

echo "=== Setting up dev environment (Kind cluster) ==="
make dev-cluster

echo "=== Starting containerized Proxmox ==="
make dev-proxmox

echo "=== Building and deploying operator ==="
make dev-deploy

echo "=== Running e2e tests ==="
make test-e2e
