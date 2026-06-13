# SPDX-License-Identifier: AGPL-3.0-or-later
BINARY      := terraform-provider-proxmox-services
VERSION     ?= dev
DEV_BIN_DIR ?= $(HOME)/.local/bin

.PHONY: build install fmt vet test tidy check clean

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

# Install where the dev_overrides .tfrc points:
#   provider_installation { dev_overrides { "jamesonrgrieve/proxmox-services" = "<DEV_BIN_DIR>" } direct {} }
install: build
	mkdir -p $(DEV_BIN_DIR)
	install -m 0755 $(BINARY) $(DEV_BIN_DIR)/$(BINARY)

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

check: tidy fmt vet test build
	@test -z "$$(gofmt -l .)" || (echo "gofmt: files need formatting" && gofmt -l . && exit 1)

clean:
	rm -f $(BINARY)
	rm -rf dist
