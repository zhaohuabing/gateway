# This is a wrapper to build golang binaries
#
# All make targets related to golang are defined in this file.

VERSION_PACKAGE := github.com/envoyproxy/gateway/internal/cmd/version

GO_LD_FLAGS += -X $(VERSION_PACKAGE).envoyGatewayVersion=$(shell cat VERSION) \
	-X $(VERSION_PACKAGE).shutdownManagerVersion=$(TAG) \
	-X $(VERSION_PACKAGE).gitCommitID=$(GIT_COMMIT)

GIT_COMMIT:=$(shell git rev-parse HEAD)

GOPATH := $(shell go env GOPATH)
ifeq ($(origin GOBIN), undefined)
	GOBIN := $(GOPATH)/bin
endif

GO_VERSION = $(shell grep -oE "^go [[:digit:]]*\.[[:digit:]]*" go.mod | cut -d' ' -f2)

DEBUG ?= false

# as per https://projectcontour.io/docs/1.24/guides/fips/
FIPS_BUILD_FLAGS = CGO_ENABLED=1 GOEXPERIMENT=boringcrypto VERIFY_FIPS=true
FIPS_LD_FLAGS = GO_LD_FLAGS
ifneq ($(DEBUG),true)
  FIPS_LD_FLAGS += -extldflags -static -s -w -linkmode=external
endif

# Build the target binary in target platform.
# The pattern of build.% is `build.{Platform}.{Command}`.
# If we want to build envoy-gateway in linux amd64 platform,
# just execute make go.build.linux_amd64.envoy-gateway.
.PHONY: go.build.%
go.build.%:
	@$(LOG_TARGET)
	$(eval COMMAND := $(word 2,$(subst ., ,$*)))
	$(eval PLATFORM := $(word 1,$(subst ., ,$*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@$(call log, "Building binary $(COMMAND) with commit $(REV) for $(OS) $(ARCH)")
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -o $(OUTPUT_DIR)/$(OS)/$(ARCH)/$(COMMAND) -ldflags "$(GO_LD_FLAGS)" $(ROOT_PACKAGE)/cmd/$(COMMAND)

.PHONY: go.fips.build.%
go.fips.build.%:
	@$(LOG_TARGET)
	$(eval COMMAND := $(word 2,$(subst ., ,$*)))
	$(eval PLATFORM := $(word 1,$(subst ., ,$*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@$(call log, "Building binary $(COMMAND) with commit $(REV) for $(OS) $(ARCH)")
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) $(FIPS_BUILD_FLAGS) go build -o $(OUTPUT_DIR)/$(OS)/$(ARCH)/$(COMMAND) -ldflags "$(FIPS_LD_FLAGS)" $(ROOT_PACKAGE)/cmd/$(COMMAND)

go.fips.verify.%:
	@$(LOG_TARGET)
	$(eval COMMAND := $(word 2,$(subst ., ,$*)))
	$(eval PLATFORM := $(word 1,$(subst ., ,$*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@$(call log, "Verifying binary $(COMMAND)")
	tools/hack/verify_fips.sh $(OUTPUT_DIR)/$(OS)/$(ARCH)/$(COMMAND)

# Build the envoy-gateway binaries in the hosted platforms.
.PHONY: go.build
go.build: $(addprefix go.build., $(addprefix $(PLATFORM)., $(BINS)))

# Build the FIPS envoy-gateway binaries in the hosted platforms.
.PHONY: go.fips.build
go.fips.build: $(addprefix go.fips.build., $(addprefix $(PLATFORM)., $(BINS))) $(addprefix go.fips.verify., $(addprefix $(PLATFORM)., $(BINS)))

# Build the envoy-gateway binaries in multi platforms
# It will build the linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 binaries out.
.PHONY: go.build.multiarch
go.build.multiarch: $(foreach p,$(PLATFORMS),$(addprefix go.build., $(addprefix $(p)., $(BINS))))

# Build the FIPS envoy-gateway binaries in multi platforms.
.PHONY: go.fips.build.multiarch
go.fips.build.multiarch: $(foreach p,$(PLATFORMS),$(addprefix go.fips.build., $(addprefix $(p)., $(BINS)))) $(foreach p,$(PLATFORMS),$(addprefix go.fips.verify., $(addprefix $(p)., $(BINS))))

.PHONY: go.test.unit
go.test.unit: ## Run go unit tests
	go test -race ./...

.PHONY: go.testdata.complete
go.testdata.complete: ## Override test ouputdata
	@$(LOG_TARGET)
	go test -timeout 30s github.com/envoyproxy/gateway/internal/xds/translator --override-testdata=true
	go test -timeout 30s github.com/envoyproxy/gateway/internal/cmd/egctl --override-testdata=true
	go test -timeout 30s github.com/envoyproxy/gateway/internal/infrastructure/kubernetes/ratelimit --override-testdata=true
	go test -timeout 30s github.com/envoyproxy/gateway/internal/infrastructure/kubernetes/proxy --override-testdata=true
	go test -timeout 30s github.com/envoyproxy/gateway/internal/xds/bootstrap --override-testdata=true
	go test -timeout 60s github.com/envoyproxy/gateway/internal/gatewayapi --override-testdata=true

.PHONY: go.test.coverage
go.test.coverage: go.test.cel ## Run go unit and integration tests in GitHub Actions
	@$(LOG_TARGET)
	KUBEBUILDER_ASSETS="$(shell $(tools/setup-envtest) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test ./... --tags=integration -race -coverprofile=coverage.xml -covermode=atomic

.PHONY: go.test.cel
go.test.cel: manifests $(tools/setup-envtest) # Run the CEL validation tests
	@$(LOG_TARGET)
	@for ver in $(ENVTEST_K8S_VERSIONS); do \
  		echo "Run CEL Validation on k8s $$ver"; \
        go clean -testcache; \
        KUBEBUILDER_ASSETS="$(shell $(tools/setup-envtest) use $$ver -p path)" \
         go test ./test/cel-validation --tags celvalidation -race; \
    done

.PHONY: go.clean
go.clean: ## Clean the building output files
	@$(LOG_TARGET)
	rm -rf $(OUTPUT_DIR)

.PHONY: go.mod.lint
lint: go.mod.lint
go.mod.lint:
	@$(LOG_TARGET)
	@go mod tidy -compat=$(GO_VERSION)
	@if test -n "$$(git status -s -- go.mod go.sum)"; then \
		git diff --exit-code go.mod; \
		git diff --exit-code go.sum; \
		$(call errorlog, "Error: ensure all changes have been committed!"); \
		exit 1; \
	else \
		$(call log, "Go module looks clean!"); \
   	fi

.PHONY: go.generate
go.generate: ## Generate code from templates
	@$(LOG_TARGET)
	go generate ./...

##@ Golang

.PHONY: build
build: ## Build envoy-gateway for host platform. See Option PLATFORM and BINS.
build: go.build

.PHONY: fips.build
fips.build: ## Build FIPS envoy-gateway for host platform. See Option PLATFORM and BINS.
fips.build: go.fips.build

.PHONY: build-multiarch
build-multiarch: ## Build envoy-gateway for multiple platforms. See Option PLATFORMS and IMAGES.
build-multiarch: go.build.multiarch

.PHONY: fips.build-multiarch
fips.build-multiarch: ## Build FIPS envoy-gateway for multiple platforms. See Option PLATFORMS and IMAGES.
fips.build-multiarch: go.fips.build.multiarch

.PHONY: test
test: ## Run all Go test of code sources.
test: go.test.unit

.PHONY: format
format: ## Update and check dependences with go mod tidy.
format: go.mod.lint

.PHONY: clean
clean: ## Remove all files that are created during builds.
clean: go.clean

.PHONY: testdata
testdata: ## Override the testdata with new configurations.
testdata: go.testdata.complete
