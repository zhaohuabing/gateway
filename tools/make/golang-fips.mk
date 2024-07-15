
# This is a wrapper to build golang FIPS binaries

DEBUG ?= false

# as per https://projectcontour.io/docs/1.24/guides/fips/
FIPS_BUILD_FLAGS = CGO_ENABLED=1 GOEXPERIMENT=boringcrypto VERIFY_FIPS=true
FIPS_LD_FLAGS = $(GO_LDFLAGS) -X github.com/envoyproxy/gateway/internal/fips.enable=true
ifneq ($(DEBUG),true)
  FIPS_LD_FLAGS += -extldflags -static -s -w -linkmode=external
endif

.PHONY: go.fips.build.%
go.fips.build.%:
	@$(LOG_TARGET)
	$(eval COMMAND := $(word 2,$(subst ., ,$*)))
	$(eval PLATFORM := $(word 1,$(subst ., ,$*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@$(call log, "Building binary $(COMMAND) with commit $(REV) for $(OS) $(ARCH)")
	GOOS=$(OS) GOARCH=$(ARCH) $(FIPS_BUILD_FLAGS) go build -o $(OUTPUT_DIR)/$(OS)/$(ARCH)/$(COMMAND) -ldflags "$(FIPS_LD_FLAGS)" $(ROOT_PACKAGE)/cmd/$(COMMAND)

go.fips.verify.%:
	@$(LOG_TARGET)
	$(eval COMMAND := $(word 2,$(subst ., ,$*)))
	$(eval PLATFORM := $(word 1,$(subst ., ,$*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@$(call log, "Verifying binary $(COMMAND)")
	tools/hack/verify_fips.sh $(OUTPUT_DIR)/$(OS)/$(ARCH)/$(COMMAND)

# Build the FIPS envoy-gateway binaries in the hosted platforms.
.PHONY: go.fips.build
go.fips.build: $(addprefix go.fips.build., $(addprefix $(PLATFORM)., $(BINS))) $(addprefix go.fips.verify., $(addprefix $(PLATFORM)., $(BINS)))

# Build the FIPS envoy-gateway binaries in multi platforms.
.PHONY: go.fips.build.multiarch
go.fips.build.multiarch: $(foreach p,$(PLATFORMS),$(addprefix go.fips.build., $(addprefix $(p)., $(BINS)))) $(foreach p,$(PLATFORMS),$(addprefix go.fips.verify., $(addprefix $(p)., $(BINS))))

##@ Golang

.PHONY: fips.build
fips.build: ## Build FIPS envoy-gateway for host platform. See Option PLATFORM and BINS.
fips.build: go.fips.build

.PHONY: fips.build-multiarch
fips.build-multiarch: ## Build FIPS envoy-gateway for multiple platforms. See Option PLATFORMS and IMAGES.
fips.build-multiarch: go.fips.build.multiarch
