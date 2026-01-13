# RHDH Must-Gather Tool Makefile

# Variables
VERSION ?= 1.9.0
GIT_SHA := $(shell git describe --no-match --always --abbrev=9 --dirty --broken 2>/dev/null || echo unknown)
RHDH_MUST_GATHER_VERSION := $(VERSION)-$(GIT_SHA)
SCRIPT ?=
REGISTRY ?= quay.io
IMAGE_NAME ?= rhdh-community/rhdh-must-gather
IMAGE_TAG ?= latest
FULL_IMAGE_NAME ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
LOG_LEVEL ?= info
OPTS ?= ## Additional options to pass to must-gather (e.g., --with-heap-dumps --with-secrets)
OVERLAY ?= ## Overlay to use for deploy-k8s (e.g., "with-heap-dumps", "debug-mode", or path to custom overlay)
CONTAINER_TOOL ?= podman
BUILD_ARGS ?=
LABELS ?=
TOOLS_DIR ?= ./bin

# Test configuration
BATS_VERSION := 1.13.0
BATS_CORE_URL := https://github.com/bats-core/bats-core/archive/refs/tags/v$(BATS_VERSION).tar.gz
BATS_BIN := $(TOOLS_DIR)/bats-core-$(BATS_VERSION)/bin/bats
TEST_RESULTS_DIR ?= ./test-results
TESTS_OPTIONS ?= --timing --print-output-on-failure --report-formatter junit --output "$(TEST_RESULTS_DIR)"
TESTS_DIR := ./tests

# Local tools configuration
YQ_VERSION := 4.50.1
YQ_ARCHIVE_DIR := $(TOOLS_DIR)/yq-$(YQ_VERSION)
YQ_BIN_DL := $(YQ_ARCHIVE_DIR)/yq
YQ_BIN := $(TOOLS_DIR)/yq
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH := $(shell uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')

default: run-local

##@ Development

.PHONY: local-output
local-output:
	@mkdir -p ./out

.PHONY: local-setup
local-setup: $(YQ_BIN_DL) ## Download and setup required local tools (yq)

.PHONY: run-local
run-local: local-output local-setup ## Test the script locally (requires jq, kubectl, oc and cluster access)
	@echo "Testing must-gather script locally..."
	@if ! command -v kubectl >/dev/null 2>&1; then \
		echo "Error: kubectl not found. Please install kubectl to test."; \
		exit 1; \
	fi
	@echo "Running local test (requires cluster access)..."
	PATH="$(abspath $(TOOLS_DIR)):$$PATH" \
		BASE_COLLECTION_PATH=./out \
		LOG_LEVEL=$(LOG_LEVEL) \
		RHDH_MUST_GATHER_VERSION=$(RHDH_MUST_GATHER_VERSION) \
		./collection-scripts/must_gather $(OPTS)

.PHONY: run-script
run-script: local-output local-setup ## Test the specified gather-<SCRIPT> script (set the SCRIPT var)
	@if [ -z "$(SCRIPT)" ]; then \
		echo "Error: SCRIPT variable is not set. Please set the SCRIPT variable to the name of the script to test. It will then run ./collection-scripts/gather_<SCRIPT>"; \
		exit 1; \
	fi
	@echo "Testing gather-${SCRIPT} must-gather script locally..."
	@echo "Running local test (requires cluster access)..."
	PATH="$(abspath $(TOOLS_DIR)):$$PATH" \
		BASE_COLLECTION_PATH=./out \
		LOG_LEVEL=$(LOG_LEVEL)\
		RHDH_MUST_GATHER_VERSION=$(RHDH_MUST_GATHER_VERSION) \
		./collection-scripts/gather_${SCRIPT} $(OPTS)

.PHONY: run-container
run-container: image-build local-output ## Test using container (requires podman)
	@echo "Testing must-gather in container..."
	podman run --rm \
		-v $(HOME)/.kube:/home/must-gather/.kube:ro \
		-v $(PWD)/out:/must-gather \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		$(IMAGE_NAME):$(IMAGE_TAG) \
		$(OPTS)

.PHONY: test-results
test-results:
	@mkdir -p $(TEST_RESULTS_DIR)

.PHONY: test-setup
test-setup: test-results ## Download and setup the unit testing framework (BATS)
	@echo "Setting up BATS testing framework..."
	@if [ ! -d "$(TOOLS_DIR)/bats-core-$(BATS_VERSION)" ]; then \
		echo "Downloading BATS v$(BATS_VERSION)..."; \
		mkdir -p "$(TOOLS_DIR)/bats-core-$(BATS_VERSION)"; \
		curl -sL $(BATS_CORE_URL) | tar xz -C "$(TOOLS_DIR)/bats-core-$(BATS_VERSION)" --strip-components=1; \
		echo "BATS installed successfully"; \
	else \
		echo "BATS $(BATS_VERSION) already installed: $(TOOLS_DIR)/bats-core-$(BATS_VERSION)"; \
	fi

.PHONY: test
test: test-setup ## Run all unit tests
	@echo "Running BATS unit tests..."
	@$(BATS_BIN) $(TESTS_OPTIONS) $(TESTS_DIR)/*.bats

LOCAL ?= true ## Set to 'false' to run E2E tests with container image instead of local mode
.PHONY: test-e2e
test-e2e: ## Run E2E tests against a K8s cluster (requires Kind or similar)
ifneq ($(LOCAL),false)
	@echo "Running E2E tests in local mode..."
	@./tests/e2e/run-e2e-tests.sh --local
else
	@echo "Running E2E tests with image: $(FULL_IMAGE_NAME)..."
	@./tests/e2e/run-e2e-tests.sh --image "$(FULL_IMAGE_NAME)" \
		$(if $(OVERLAY),--overlay "$(OVERLAY)") \
		$(if $(TARGET_BRANCH),--target-branch "$(TARGET_BRANCH)") \
		$(if $(OPERATOR_BRANCH),--operator-branch "$(OPERATOR_BRANCH)") \
		$(if $(HELM_CHART_VERSION),--helm-chart-version "$(HELM_CHART_VERSION)") \
		$(if $(HELM_VALUES_FILE),--helm-values-file "$(HELM_VALUES_FILE)")
endif

.PHONY: $(TOOLS_DIR)
$(TOOLS_DIR):
	@mkdir -p "$(TOOLS_DIR)"

.PHONY: $(YQ_BIN_DL)
$(YQ_BIN_DL): $(TOOLS_DIR)
	@mkdir -p "$(YQ_ARCHIVE_DIR)"
	@if [ ! -f "$(YQ_BIN_DL)" ]; then \
		echo "Downloading yq v$(YQ_VERSION) for $(OS)/$(ARCH)..."; \
		curl -sSL "https://github.com/mikefarah/yq/releases/download/v$(YQ_VERSION)/yq_$(OS)_$(ARCH).tar.gz" | tar xz -C "$(TOOLS_DIR)"; \
		mv -f "$(TOOLS_DIR)/yq_$(OS)_$(ARCH)" "$(YQ_BIN_DL)"; \
		chmod +x "$(YQ_BIN_DL)"; \
		echo "yq installed successfully: $$($(YQ_BIN_DL) --version)"; \
	else \
		echo "yq $(YQ_VERSION) already installed: $(YQ_BIN_DL)"; \
	fi
	@ln -sf "$(shell echo $(YQ_BIN_DL) | sed 's|$(TOOLS_DIR)/||')" "$(YQ_BIN)"
	@"$(YQ_BIN)" --version

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
	oc adm must-gather --image=$(FULL_IMAGE_NAME) $(if $(OPTS),-- /usr/bin/gather $(OPTS))

.PHONY: deploy-k8s
deploy-k8s: ## Deploy the must-gather image on a non-OCP K8s cluster (uses Kustomize)
	@if ! command -v kubectl >/dev/null 2>&1; then \
		echo "Error: kubectl command not found. Please install kubectl."; \
		exit 1; \
	fi
	@./hack/deploy-k8s.sh --image "$(FULL_IMAGE_NAME)" $(if $(OVERLAY),--overlay "$(OVERLAY)") $(if $(OPTS),--opts "$(OPTS)")


##@ Cleanup

.PHONY: clean-out
clean-out: ## Remove the local output directory
	-rm -rf ./out
	@echo "Local output directory cleaned"

.PHONY: clean
clean: clean-out## Remove built images and test output
	@echo "Cleaning up..."
	-podman rmi $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
	-podman rmi $(FULL_IMAGE_NAME) 2>/dev/null || true
	-rm -rf "$(TOOLS_DIR)"
	-rm -rf "$(TEST_RESULTS_DIR)"
	@echo "Cleanup complete"

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
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
	@echo ""
	@echo "Variables:"
	@echo "  VERSION			- Must-gather version (default: $(VERSION))"
	@echo "  RHDH_MUST_GATHER_VERSION	- Full version with git SHA (computed: $(RHDH_MUST_GATHER_VERSION))"
	@echo "  REGISTRY			- Container registry (default: $(REGISTRY))"
	@echo "  IMAGE_NAME			- Container image name (default: $(IMAGE_NAME))"
	@echo "  IMAGE_TAG			- Container image tag (default: $(IMAGE_TAG))"
	@echo "  LOG_LEVEL			- Log level (default: $(LOG_LEVEL))"
	@echo "  OPTS				- Additional must-gather options (e.g., --with-heap-dumps --with-secrets)"
	@echo "  OVERLAY			- Kustomize overlay for deploy-k8s/test-e2e (e.g., \"with-heap-dumps\", \"debug-mode\", or path)"
	@echo "  TARGET_BRANCH			- Target branch for test-e2e defaults (default: main)"
	@echo "  OPERATOR_BRANCH		- Override RHDH operator branch for test-e2e"
	@echo "  HELM_CHART_VERSION		- Override Helm chart version for test-e2e"
	@echo "  HELM_VALUES_FILE		- Override Helm values file for test-e2e"
	@echo "  LOCAL				- Set to 'false' to run test-e2e with container image (default: true, local mode)"
	@echo "  SCRIPT			- Script name for run-script"
	@echo "  TOOLS_DIR			- Directory for local tools like yq (default: $(TOOLS_DIR))"
	@echo ""
	@echo "Examples:"
	@echo "  make test                                          # Run all unit tests"
	@echo "  make test-e2e                                      # Run E2E tests in local mode (default)"
	@echo "  make test-e2e LOCAL=false FULL_IMAGE_NAME=quay.io/org/img:tag  # Run E2E tests with container image"
	@echo "  make deploy-k8s OVERLAY=with-heap-dumps            # Run deploy-k8s with heap dump overlay"
	@echo "  make deploy-k8s OVERLAY=/path/to/my-overlay        # Run deploy-k8s with custom overlay"
	@echo "  make run-local OPTS=\"--with-heap-dumps\""
	@echo "  make run-container OPTS=\"--with-secrets --with-heap-dumps\""
	@echo "  make deploy-openshift OPTS=\"--with-heap-dumps --namespaces my-ns\""
