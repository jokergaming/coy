GO ?= go
VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)
ARTIFACTS = dist/coy_linux_amd64 dist/coy_linux_arm64 dist/coy_darwin_amd64 dist/coy_darwin_arm64 dist/coy_windows_amd64.exe

.PHONY: check test vet build dist

check: test vet build

test:
	$(GO) test -timeout 60s ./...

vet:
	$(GO) vet ./...

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/coy ./cmd/coy

dist: $(ARTIFACTS)
	cd dist && sha256sum $(notdir $(ARTIFACTS)) > SHA256SUMS

# Rebuild each artifact so a changed source tree or VERSION cannot reuse stale binaries.
.PHONY: $(ARTIFACTS)
dist/coy_linux_amd64: TARGET_OS=linux
dist/coy_linux_amd64: TARGET_ARCH=amd64
dist/coy_linux_arm64: TARGET_OS=linux
dist/coy_linux_arm64: TARGET_ARCH=arm64
dist/coy_darwin_amd64: TARGET_OS=darwin
dist/coy_darwin_amd64: TARGET_ARCH=amd64
dist/coy_darwin_arm64: TARGET_OS=darwin
dist/coy_darwin_arm64: TARGET_ARCH=arm64
dist/coy_windows_amd64.exe: TARGET_OS=windows
dist/coy_windows_amd64.exe: TARGET_ARCH=amd64

$(ARTIFACTS):
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $@ ./cmd/coy
