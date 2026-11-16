# Makefile for kube-tollgate
# Dynamic FinOps Admission Controller for Kubernetes

# Image URL to use all building/pushing image targets
IMG ?= kube-tollgate:dev
# Cluster name for local development
CLUSTER_NAME ?= kube-tollgate-dev

# Get the currently used golang install path (in GOPATH/bin, /usr/local/go/bin, or GOBIN)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

# Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

# Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.16.1
GOLANGCI_LINT_VERSION ?= v1.60.1

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=tollgate-role crd:allowDangerousTypes=true webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run unit tests.
	go test -v -race ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter against code.
	$(GOLANGCI_LINT) run ./...

##@ Build

.PHONY: build
build: fmt vet ## Build tollgate manager binary.
	go build -o bin/tollgate cmd/tollgate/main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	go run ./cmd/tollgate/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t $(IMG) .

##@ Deployment and Local Cluster (k3d)

.PHONY: cluster-up
cluster-up: ## Create a local k3d cluster for development.
	@if ! k3d cluster list | grep -q $(CLUSTER_NAME); then \
		echo "Creating k3d cluster $(CLUSTER_NAME)..."; \
		k3d cluster create $(CLUSTER_NAME) --agents 1 --port "9443:9443@loadbalancer" --port "8080:8080@loadbalancer"; \
	else \
		echo "Cluster $(CLUSTER_NAME) already exists."; \
	fi

.PHONY: cluster-down
cluster-down: ## Tear down the local k3d cluster.
	k3d cluster delete $(CLUSTER_NAME)

.PHONY: cluster-import
cluster-import: docker-build ## Import local docker image into k3d cluster.
	k3d image import $(IMG) -c $(CLUSTER_NAME)

.PHONY: deploy
deploy: manifests ## Deploy tollgate to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f config/crd/bases
	kubectl apply -f config/rbac
	kubectl apply -f config/webhook
	kubectl apply -f config/manager

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f config/manager --ignore-not-found=true
	kubectl delete -f config/webhook --ignore-not-found=true
	kubectl delete -f config/rbac --ignore-not-found=true
	kubectl delete -f config/crd/bases --ignore-not-found=true

##@ Dependencies

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
