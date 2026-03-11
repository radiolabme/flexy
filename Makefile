BINARY   := flexy
DISCOVERY := flexy-discovery
BIN      := bin
DLDIR    := cmd/flexy/web/downloads
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -ldflags "-X main.Version=$(VERSION)"

# proxy requires unix (proxy.go has //go:build unix)
PROXY_PLATFORMS     := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
DISCOVERY_PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

.PHONY: all release proxy discovery embed-downloads clean \
	$(addprefix proxy-,$(PROXY_PLATFORMS)) \
	$(addprefix discovery-,$(DISCOVERY_PLATFORMS))

# Local build (native OS/arch).
all:
	go build $(LDFLAGS) -o $(BIN)/$(BINARY) ./cmd/flexy
	go build -o $(BIN)/$(DISCOVERY) ./cmd/flexy-discovery

# Cross-compile everything. Embeds discovery binaries into the proxy.
release: embed-downloads proxy discovery

# Build discovery binaries into web/downloads/ so they get embedded in the proxy.
embed-downloads:
	@mkdir -p $(DLDIR)
	$(foreach p,$(DISCOVERY_PLATFORMS),\
		$(eval _OS   := $(word 1,$(subst -, ,$(p))))\
		$(eval _ARCH := $(word 2,$(subst -, ,$(p))))\
		$(eval _EXT  := $(if $(filter windows,$(_OS)),.exe,))\
		GOOS=$(_OS) GOARCH=$(_ARCH) go build -o $(DLDIR)/$(DISCOVERY)-$(p)$(_EXT) ./cmd/flexy-discovery ;)

proxy: $(addprefix proxy-,$(PROXY_PLATFORMS))
discovery: $(addprefix discovery-,$(DISCOVERY_PLATFORMS))

# --- proxy targets (unix only) ---
define PROXY_RULE
proxy-$(1):
	$$(eval OS   := $$(word 1,$$(subst -, ,$(1))))
	$$(eval ARCH := $$(word 2,$$(subst -, ,$(1))))
	GOOS=$$(OS) GOARCH=$$(ARCH) go build $$(LDFLAGS) -o $$(BIN)/$$(BINARY)-$(1) ./cmd/flexy
endef
$(foreach p,$(PROXY_PLATFORMS),$(eval $(call PROXY_RULE,$(p))))

# --- discovery targets (all platforms) ---
define DISCOVERY_RULE
discovery-$(1):
	$$(eval OS   := $$(word 1,$$(subst -, ,$(1))))
	$$(eval ARCH := $$(word 2,$$(subst -, ,$(1))))
	$$(eval EXT  := $$(if $$(filter windows,$$(OS)),.exe,))
	GOOS=$$(OS) GOARCH=$$(ARCH) go build -o $$(BIN)/$$(DISCOVERY)-$(1)$$(EXT) ./cmd/flexy-discovery
endef
$(foreach p,$(DISCOVERY_PLATFORMS),$(eval $(call DISCOVERY_RULE,$(p))))

# --- install on remote host ---
# Usage: make install HOST=user@192.168.1.x
install: proxy-linux-amd64
	@test -n "$(HOST)" || (echo "usage: make install HOST=user@host"; exit 1)
	ssh $(HOST) "mkdir -p /usr/local/bin"
	scp $(BIN)/$(BINARY)-linux-amd64 $(HOST):/usr/local/bin/$(BINARY)
	scp flexy.service $(HOST):/etc/systemd/system/flexy.service
	ssh $(HOST) "systemctl daemon-reload && systemctl enable flexy && systemctl restart flexy"

install-arm64: proxy-linux-arm64
	@test -n "$(HOST)" || (echo "usage: make install-arm64 HOST=user@host"; exit 1)
	ssh $(HOST) "mkdir -p /usr/local/bin"
	scp $(BIN)/$(BINARY)-linux-arm64 $(HOST):/usr/local/bin/$(BINARY)
	scp flexy.service $(HOST):/etc/systemd/system/flexy.service
	ssh $(HOST) "systemctl daemon-reload && systemctl enable flexy && systemctl restart flexy"

clean:
	rm -rf $(BIN) $(DLDIR)
