
# Image URL to use all building/pushing image targets
IMG ?= alperencelik/kubemox:latest 
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.27.1

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /test/e2e) -coverprofile cover.out

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish built the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64 ). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for  the manager image be build to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - able to use docker buildx . More info: https://docs.docker.com/build/buildx/
# - have enable BuildKit, More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image for your registry (i.e. if you do not inform a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To properly provided solutions that supports more than one platform you should use this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: test ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name project-v3-builder
	$(CONTAINER_TOOL) buildx use project-v3-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm project-v3-builder
	rm Dockerfile.cross

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v5.2.1
CONTROLLER_TOOLS_VERSION ?= v0.17.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary. If wrong version is installed, it will be removed before downloading.
$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || GOBIN=$(LOCALBIN) GO111MODULE=on go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: custom-dashboard
custom-dashboard: ## Run the custom dashboard generator. For more information check out https://kubebuilder.io/plugins/available/grafana-v1-alpha
	kubebuilder edit --plugins grafana.kubebuilder.io/v1-alpha

.PHONY: chart
chart: manifests ## Copies and cleans CRDs for the Helm chart.
	@echo "--- Helm: Cleaning and copying CRDs"
	@./scripts/clean_crds.sh config/crd/bases charts/kubemox/templates/crds

##@ Dev Environment

KIND_CLUSTER_NAME ?= kubemox-dev
KIND_CONFIG ?= hack/kind-config.yaml
KIND_NODE_IMAGE ?= kindest/node:v1.34.0
PROXMOX_CONTAINER_NAME ?= proxmox-dev
PROXMOX_IMAGE ?= ghcr.io/longqt-sea/proxmox-ve:latest
DEV_HELM_VALUES ?= hack/observability/dev-values.yaml

# Images to pre-pull and cache for dev environment
DEV_IMAGES ?= \
	docker.io/grafana/grafana:12.4.2 \
	quay.io/kiwigrid/k8s-sidecar:2.5.4 \
	quay.io/prometheus-operator/prometheus-operator:v0.89.0 \
	quay.io/prometheus-operator/prometheus-config-reloader:v0.89.0 \
	quay.io/prometheus/prometheus:v3.11.0

.PHONY: dev-cluster
dev-cluster: ## Create Kind cluster for local development
	@if kind get clusters 2>/dev/null | grep -q $(KIND_CLUSTER_NAME); then \
		echo "Cluster $(KIND_CLUSTER_NAME) already exists"; \
	else \
		kind create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_NODE_IMAGE) --config $(KIND_CONFIG); \
	fi
	@echo "Pre-loading dev images into Kind..."
	@for img in $(DEV_IMAGES); do \
		$(CONTAINER_TOOL) pull $$img 2>/dev/null || true; \
	done
	@kind load docker-image $(DEV_IMAGES) --name $(KIND_CLUSTER_NAME) 2>/dev/null || true

.PHONY: dev-cluster-delete
dev-cluster-delete: ## Delete Kind development cluster
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: dev-proxmox
dev-proxmox: ## Start containerized Proxmox VE on the Kind Docker network
	@if $(CONTAINER_TOOL) ps -a --format '{{.Names}}' | grep -q '^$(PROXMOX_CONTAINER_NAME)$$'; then \
		echo "Container $(PROXMOX_CONTAINER_NAME) already exists, starting..."; \
		$(CONTAINER_TOOL) start $(PROXMOX_CONTAINER_NAME); \
	else \
		$(CONTAINER_TOOL) run -d \
			--name $(PROXMOX_CONTAINER_NAME) \
			--hostname $(PROXMOX_CONTAINER_NAME) \
			--network kind \
			--privileged \
			-p 8006:8006 \
			-p 8022:22 \
			--env PASSWORD=123 \
			$(PROXMOX_IMAGE); \
	fi
	@echo "Waiting for Proxmox API to be ready..."
	@for i in $$(seq 1 60); do \
		if curl -sk https://localhost:8006/api2/json/version >/dev/null 2>&1; then \
			echo "Proxmox API is ready at https://localhost:8006"; \
			break; \
		fi; \
		if [ $$i -eq 60 ]; then echo "Timeout waiting for Proxmox API"; exit 1; fi; \
		sleep 5; \
	done

.PHONY: dev-proxmox-stop
dev-proxmox-stop: ## Stop and remove containerized Proxmox VE
	-$(CONTAINER_TOOL) rm -f $(PROXMOX_CONTAINER_NAME)

.PHONY: dev-observability
dev-observability: ## Install Prometheus + Grafana observability stack
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
	helm repo update
	helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
		--namespace monitoring --create-namespace \
		-f hack/observability/prometheus-values.yaml \
		--wait --timeout 5m

.PHONY: dev-grafana-dashboard
dev-grafana-dashboard: ## Install Grafana dashboards (operator overview + controller-runtime)
	$(KUBECTL) create configmap kubemox-operator-dashboard \
		--from-file=kubemox-operator.json=hack/observability/grafana-dashboard.json \
		-n monitoring --dry-run=client -o yaml | \
		$(KUBECTL) label --local -f - grafana_dashboard=1 -o yaml | \
		$(KUBECTL) annotate --local -f - grafana_folder=Kubemox -o yaml | \
		$(KUBECTL) apply -f -
	sed 's/$${DS_PROMETHEUS}/Prometheus/g' grafana/controller-runtime-metrics.json > /tmp/controller-runtime-dashboard.json
	$(KUBECTL) create configmap kubemox-controller-runtime-dashboard \
		--from-file=controller-runtime.json=/tmp/controller-runtime-dashboard.json \
		-n monitoring --dry-run=client -o yaml | \
		$(KUBECTL) label --local -f - grafana_dashboard=1 -o yaml | \
		$(KUBECTL) annotate --local -f - grafana_folder=Kubemox -o yaml | \
		$(KUBECTL) apply -f -

.PHONY: docker-build-dev
docker-build-dev: ## Build docker image without running tests (for dev/e2e use)
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: dev-deploy
dev-deploy: docker-build-dev ## Build, load into Kind, and deploy operator with dev settings
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)
	helm upgrade --install kubemox charts/kubemox \
		-f $(DEV_HELM_VALUES) \
		--set image.repository=$(firstword $(subst :, ,$(IMG))) \
		--set image.tag=$(lastword $(subst :, ,$(IMG))) \
		--wait --timeout 3m

.PHONY: dev-setup
dev-setup: dev-cluster dev-observability dev-grafana-dashboard ## Full dev environment setup (cluster + observability + dashboards)
	@echo ""
	@echo "=== Dev environment is ready ==="
	@echo "Grafana:    http://localhost:30300 (admin/admin)"
	@echo "Prometheus: http://localhost:30900"
	@echo ""
	@echo "Next steps:"
	@echo "  make dev-proxmox   # Start containerized Proxmox"
	@echo "  make dev-deploy    # Build and deploy operator"
	@echo "  make test-e2e      # Run e2e tests"

.PHONY: dev-all
dev-all: dev-setup dev-proxmox dev-deploy ## Full dev environment: cluster + observability + Proxmox + operator
	@echo ""
	@echo "============================================="
	@echo "  Kubemox Dev Environment is fully running!"
	@echo "============================================="
	@echo ""
	@echo "  Grafana:    http://localhost:30300 (admin/admin)"
	@echo "  Prometheus: http://localhost:30900"
	@echo "  Proxmox:    https://localhost:8006 (root/123)"
	@echo ""
	@echo "  Run 'make dev-status' to verify the stack."
	@echo ""

.PHONY: dev-status
dev-status: ## Verify the dev environment stack is healthy
	@echo "=== Kind Cluster ==="
	@kind get clusters 2>/dev/null | grep -q $(KIND_CLUSTER_NAME) && echo "  ✓ Cluster $(KIND_CLUSTER_NAME) exists" || echo "  ✗ Cluster not found"
	@echo ""
	@echo "=== Operator ==="
	@$(KUBECTL) get pods -l app.kubernetes.io/name=kubemox -o wide 2>/dev/null || echo "  ✗ Operator not deployed"
	@echo ""
	@echo "=== Metrics Endpoint ==="
	@$(KUBECTL) get servicemonitor -A 2>/dev/null | grep -i kubemox && echo "  ✓ ServiceMonitor found" || echo "  ✗ No ServiceMonitor"
	@echo ""
	@echo "=== Observability Stack ==="
	@$(KUBECTL) get pods -n monitoring -l app.kubernetes.io/name=grafana -o wide 2>/dev/null | head -5 || echo "  ✗ Grafana not running"
	@$(KUBECTL) get pods -n monitoring -l app.kubernetes.io/name=prometheus -o wide 2>/dev/null | head -5 || echo "  ✗ Prometheus not running"
	@echo ""
	@echo "=== Grafana Dashboards ==="
	@$(KUBECTL) get configmap -n monitoring -l grafana_dashboard=1 --no-headers 2>/dev/null || echo "  ✗ No dashboards installed"
	@echo ""
	@echo "=== Proxmox Container ==="
	@$(CONTAINER_TOOL) ps --filter name=$(PROXMOX_CONTAINER_NAME) --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' 2>/dev/null || echo "  ✗ Proxmox not running"
	@echo ""
	@echo "=== Access URLs ==="
	@echo "  Grafana:    http://localhost:30300"
	@echo "  Prometheus: http://localhost:30900"
	@echo "  Proxmox:    https://localhost:8006"

.PHONY: dev-teardown
dev-teardown: ## Tear down full dev environment
	-$(CONTAINER_TOOL) rm -f $(PROXMOX_CONTAINER_NAME)
	-kind delete cluster --name $(KIND_CLUSTER_NAME)

##@ E2E Testing

E2E_CONFIG ?= test/e2e/config/e2e-config.yaml
E2E_TIMEOUT ?= 30m
GINKGO_TIMEOUT ?= 25m

.PHONY: test-e2e
test-e2e: ## Run e2e tests against an existing cluster
	go test ./test/e2e/ -v -timeout $(E2E_TIMEOUT) -count=1 \
		-ginkgo.v -ginkgo.timeout=$(GINKGO_TIMEOUT)

.PHONY: test-e2e-local
test-e2e-local: dev-setup dev-proxmox dev-deploy test-e2e ## Full local e2e: setup, deploy, test, teardown
	$(MAKE) dev-teardown
