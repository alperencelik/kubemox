#!/usr/bin/env bash
# Convenience script for running the full e2e test lifecycle.
# Usage: ./hack/e2e.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

cleanup() {
    echo "=== Tearing down dev environment ==="
    make dev-teardown || true
}
trap cleanup EXIT

echo "=== Setting up dev environment (Kind + CRDs + observability) ==="
make dev-setup

echo "=== Starting containerized Proxmox ==="
make dev-proxmox

echo "=== Building and deploying operator ==="
make dev-deploy

echo "=== Running e2e tests ==="
make test-e2e
