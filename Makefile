# RHDH Must-Gather Tool Makefile

# Variables
VERSION ?= 2.1.0
GIT_SHA := $(shell git describe --no-match --always --abbrev=9 --dirty --broken 2>/dev/null || echo unknown)
RHDH_MUST_GATHER_VERSION := $(VERSION)-$(GIT_SHA)
REGISTRY ?= quay.io
IMAGE_NAME ?= rhdh-community/rhdh-must-gather
IMAGE_TAG ?= latest
FULL_IMAGE_NAME ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
LOG_LEVEL ?= info
OPTS ?= ## Additional options to pass to must-gather (e.g., --with-heap-dumps --with-secrets)
NAMESPACE ?= ## Namespace for deploy-k8s/deploy-openshift (default: random for k8s, auto for openshift)
HELM_SET ?= ## Additional Helm --set flags for deploy-k8s (e.g., "gather.logLevel=debug")
OUTPUT_FILE ?= ## Output file for deploy-k8s (default: rhdh-must-gather-output.k8s.<timestamp>.tar.gz)
HELM_TIMEOUT ?= ## Timeout for Helm install/upgrade in deploy-k8s (default: 60m)
CONTAINER_TOOL ?= podman
BUILD_ARGS ?=
LABELS ?=
TOOLS_DIR ?= ./bin
BASE_COLLECTION_PATH ?= ./out

# Go configuration
GO ?= go
GO_MODULE := github.com/redhat-developer/rhdh-must-gather
GO_BUILD_FLAGS ?= -trimpath -mod=mod
GO_LDFLAGS := -X '$(GO_MODULE)/internal/cli.version=$(RHDH_MUST_GATHER_VERSION)'
GO_BIN := $(TOOLS_DIR)/gather


default: run-local

##@ Development

.PHONY: local-output
local-output:
	@mkdir -p ./out

.PHONY: run-local
run-local: local-output go-build ## Run the Go gather binary locally (requires cluster access)
	@echo "Running must-gather locally..."
	BASE_COLLECTION_PATH=$(BASE_COLLECTION_PATH) \
		LOG_LEVEL=$(LOG_LEVEL) \
		RHDH_MUST_GATHER_VERSION=$(RHDH_MUST_GATHER_VERSION) \
		$(GO_BIN) $(OPTS)

LOCAL ?= true ## Set to 'false' to run E2E tests with container image instead of local mode
WITH_HEAP_DUMPS ?= ## Set to 'true' to enable heap dump collection and validation in E2E tests
HEAP_DUMP_METHOD ?= ## Heap dump method: 'inspector' (default) or 'sigusr2'
.PHONY: test-e2e
test-e2e: ## Run E2E tests against a K8s cluster (requires Kind or similar)
ifneq ($(LOCAL),false)
	@echo "Running E2E tests in local mode..."
	@./tests/e2e/run-e2e-tests.sh --local \
		$(if $(filter true,$(WITH_HEAP_DUMPS)),--with-heap-dumps) \
		$(if $(HEAP_DUMP_METHOD),--heap-dump-method "$(HEAP_DUMP_METHOD)") \
		$(if $(HELM_TIMEOUT),--helm-timeout "$(HELM_TIMEOUT)")
else
	@echo "Running E2E tests with image: $(FULL_IMAGE_NAME)..."
	@./tests/e2e/run-e2e-tests.sh --image "$(FULL_IMAGE_NAME)" \
		$(if $(TARGET_BRANCH),--target-branch "$(TARGET_BRANCH)") \
		$(if $(OPERATOR_BRANCH),--operator-branch "$(OPERATOR_BRANCH)") \
		$(if $(HELM_CHART_VERSION),--helm-chart-version "$(HELM_CHART_VERSION)") \
		$(if $(HELM_VALUES_FILE),--helm-values-file "$(HELM_VALUES_FILE)") \
		$(if $(filter true,$(WITH_HEAP_DUMPS)),--with-heap-dumps) \
		$(if $(HEAP_DUMP_METHOD),--heap-dump-method "$(HEAP_DUMP_METHOD)") \
		$(if $(HELM_TIMEOUT),--helm-timeout "$(HELM_TIMEOUT)")
endif

.PHONY: $(TOOLS_DIR)
$(TOOLS_DIR):
	@mkdir -p "$(TOOLS_DIR)"

##@ Go

.PHONY: go-build
go-build: $(TOOLS_DIR) ## Build the Go gather binary
	$(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -o $(GO_BIN) ./cmd/gather

.PHONY: test
test: ## Run unit tests
	$(GO) test -mod=mod ./... -v -count=1

.PHONY: lint
lint: ## Run linter (golangci-lint)
	golangci-lint run ./...

##@ Build

.PHONY: image-build
image-build: ## Build the must-gather container image
	@echo "Building must-gather image..."
	$(CONTAINER_TOOL) build $(BUILD_ARGS) $(if $(LABELS),$(LABELS)) --build-arg RHDH_MUST_GATHER_VERSION=$(RHDH_MUST_GATHER_VERSION) -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "Image built: $(IMAGE_NAME):$(IMAGE_TAG)"

.PHONY: image-push
image-push: image-build ## Build and push the image to registry
	@echo "Tagging image for registry..."
	$(CONTAINER_TOOL) tag $(IMAGE_NAME):$(IMAGE_TAG) $(FULL_IMAGE_NAME)
	@echo "Pushing image to registry..."
	$(CONTAINER_TOOL) push $(FULL_IMAGE_NAME)
	@echo "Image pushed: $(FULL_IMAGE_NAME)"


##@ Deployment

.PHONY: deploy-openshift
deploy-openshift: ## Deploy the must-gather image using the 'oc adm must-gather' command
	@echo "Deploying the must-gather image with oc adm must-gather..."
	@if ! command -v oc >/dev/null 2>&1; then \
		echo "Error: oc command not found. Please install OpenShift CLI."; \
		exit 1; \
	fi
	oc adm must-gather --image=$(FULL_IMAGE_NAME) $(if $(NAMESPACE),--run-namespace=$(NAMESPACE)) $(if $(OPTS),-- /usr/bin/gather $(OPTS))

.PHONY: deploy-k8s
deploy-k8s: ## Deploy the must-gather image on a non-OCP K8s cluster (uses Helm chart)
	@if ! command -v kubectl >/dev/null 2>&1; then \
		echo "Error: kubectl command not found. Please install kubectl."; \
		exit 1; \
	fi
	@if ! command -v helm >/dev/null 2>&1; then \
		echo "Error: helm command not found. Please install Helm."; \
		exit 1; \
	fi
	@./hack/deploy-k8s.sh --image "$(FULL_IMAGE_NAME)" $(if $(NAMESPACE),--namespace "$(NAMESPACE)") $(if $(OPTS),--opts "$(OPTS)") $(if $(HELM_SET),--helm-set "$(HELM_SET)") $(if $(OUTPUT_FILE),--output "$(OUTPUT_FILE)") $(if $(HELM_TIMEOUT),--timeout "$(HELM_TIMEOUT)")


##@ Cleanup

.PHONY: clean-out
clean-out: ## Remove the local output directory
	-rm -rf ./out
	@echo "Local output directory cleaned"

.PHONY: clean
clean: clean-out ## Remove built images and test output
	@echo "Cleaning up..."
	-podman rmi $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
	-podman rmi $(FULL_IMAGE_NAME) 2>/dev/null || true
	-rm -rf "$(TOOLS_DIR)"
	-rm -rf "$(TEST_RESULTS_DIR)"
	@echo "Cleanup complete"

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
	@echo ""
	@echo "Variables:"
	@echo "  VERSION			- Must-gather version (default: $(VERSION))"
	@echo "  RHDH_MUST_GATHER_VERSION	- Full version with git SHA (computed: $(RHDH_MUST_GATHER_VERSION))"
	@echo "  REGISTRY			- Container registry (default: $(REGISTRY))"
	@echo "  IMAGE_NAME			- Container image name (default: $(IMAGE_NAME))"
	@echo "  IMAGE_TAG			- Container image tag (default: $(IMAGE_TAG))"
	@echo "  LOG_LEVEL			- Log level (default: $(LOG_LEVEL))"
	@echo "  OPTS				- Additional must-gather options (e.g., --with-heap-dumps --with-secrets)"
	@echo "  NAMESPACE			- Namespace for deploy-k8s/deploy-openshift (default: random for k8s, auto for openshift)"
	@echo "  HELM_SET			- Additional Helm --set flags for deploy-k8s (e.g., \"gather.logLevel=debug\")"
	@echo "  OUTPUT_FILE			- Output file for deploy-k8s (default: rhdh-must-gather-output.k8s.<timestamp>.tar.gz)"
	@echo "  HELM_TIMEOUT			- Timeout for Helm install/upgrade in deploy-k8s (default: 60m)"
	@echo "  TARGET_BRANCH			- Target branch for test-e2e defaults (default: main)"
	@echo "  OPERATOR_BRANCH		- Override RHDH operator branch for test-e2e"
	@echo "  HELM_CHART_VERSION		- Override Helm chart version for test-e2e"
	@echo "  HELM_VALUES_FILE		- Override Helm values file for test-e2e"
	@echo "  LOCAL				- Set to 'false' to run test-e2e with container image (default: true, local mode)"
	@echo ""
	@echo "Examples:"
	@echo "  make test                                          # Run all unit tests"
	@echo "  make test-e2e                                      # Run E2E tests in local mode (default)"
	@echo "  make test-e2e LOCAL=false FULL_IMAGE_NAME=quay.io/org/img:tag  # Run E2E tests with container image"
	@echo "  make deploy-k8s OPTS=\"--with-heap-dumps\"           # Run deploy-k8s with heap dumps"
	@echo "  make deploy-k8s NAMESPACE=my-ns                    # Run deploy-k8s in a specific namespace"
	@echo "  make run-local OPTS=\"--with-heap-dumps\""
	@echo "  make deploy-openshift OPTS=\"--with-heap-dumps --namespaces my-ns\""
