.PHONY: build test lint e2e e2e-critical release

COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet HEAD 2>/dev/null || echo -dirty)
BIN ?= bin/admiral

build:
	npm --prefix web run build
	go build -trimpath -ldflags "-X main.commit=$(COMMIT)" -o $(BIN) ./cmd/admiral

test:
	go test -count=1 $$(go list ./... | grep -v /e2e)
	npm --prefix web test

lint:
	go vet ./...
	npm --prefix web run typecheck

e2e: build
	go test -count=1 ./e2e/...
	npm --prefix web run e2e

# What CI runs: the Go e2e tests and the @critical browser flows on the iPad
# viewport. The CI runner's virtual GPU resets under WebKit about 40 times a
# minute, so the full matrix flakes there (#94); it runs locally before a PR.
e2e-critical: build
	go test -count=1 ./e2e/...
	npm --prefix web run e2e -- --project ipad-landscape --grep @critical

# The release: a clean UI build, the darwin/arm64 binary stamped with the
# version and commit, and the tarball with its checksum in $(DIST). A shared
# build never carries official logos, so the target refuses when the folder
# holds any. Never strip the binary: the Go linker's ad-hoc signature covers it.
# One version rule: tag v<x.y.z>, version <x.y.z>. A leading v is dropped here,
# once, so the tarball name, --version, install.sh and the formula agree.
# VERSION and OFFICIAL_LOGOS come from the command line only, never the
# environment; BIN and DIST may come from either.
ifeq ($(origin VERSION),command line)
RAW_VERSION := $(VERSION)
else
RAW_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
override VERSION := $(patsubst v%,%,$(RAW_VERSION))
DIST ?= dist
OFFICIAL_LOGOS = web/logos/official
RELEASE_NAME = admiral-$(VERSION)-darwin-arm64

release:
	@test -n "$(VERSION)" || { echo "make release: VERSION is empty; give a version or build from a git checkout" >&2; exit 1; }
	@for d in '$(OFFICIAL_LOGOS)' web/logos/official; do \
		if [ -n "$$(find -L "$$d" ! -type d ! -name '.*' 2>/dev/null)" ]; then \
		echo "make release: $$d holds official logos; a shared build ships the placeholders. Move them away and run again." >&2; exit 1; fi; done
	rm -rf web/dist
	npm --prefix web run build
	@for l in Cl Co Cx; do grep -rq "\"$$l\"" web/dist/assets || { echo "make release: the built UI has no $$l placeholder logo" >&2; exit 1; }; done
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o $(BIN) ./cmd/admiral
	codesign -v $(BIN)
	rm -rf $(DIST)/$(RELEASE_NAME) $(DIST)/$(RELEASE_NAME).tar.gz $(DIST)/$(RELEASE_NAME).tar.gz.sha256
	mkdir -p $(DIST)/$(RELEASE_NAME)/plugins
	cp $(BIN) $(DIST)/$(RELEASE_NAME)/admiral
	cp -R plugins/freeform $(DIST)/$(RELEASE_NAME)/plugins/freeform
	cp -R plugins/github $(DIST)/$(RELEASE_NAME)/plugins/github
	cp LICENSE $(DIST)/$(RELEASE_NAME)/LICENSE
	sh packaging/notices.sh > $(DIST)/$(RELEASE_NAME)/THIRD_PARTY_NOTICES
	COPYFILE_DISABLE=1 tar --uid 0 --gid 0 --uname "" --gname "" -czf $(DIST)/$(RELEASE_NAME).tar.gz -C $(DIST) $(RELEASE_NAME)
	cp packaging/install.sh $(DIST)/install.sh
	cd $(DIST) && shasum -a 256 $(RELEASE_NAME).tar.gz > $(RELEASE_NAME).tar.gz.sha256
	rm -rf $(DIST)/$(RELEASE_NAME)
	@echo "$(DIST)/$(RELEASE_NAME).tar.gz"
