BINARY := cx
PREFIX ?= $(HOME)/.local

# Stamp the binary so `cx version` can identify it. Falls back to the version
# and VCS stamps the Go toolchain records when this is empty, so a build from a
# tarball without a .git directory still reports something useful.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test fmt vet lint install clean run

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/cx

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: fmt vet test

install: build
	mkdir -p $(PREFIX)/bin
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "installed to $(PREFIX)/bin/$(BINARY)"

run:
	go run ./cmd/cx

clean:
	rm -f $(BINARY)
