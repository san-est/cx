BINARY := cx
PREFIX ?= $(HOME)/.local

.PHONY: build test fmt vet lint install clean run

build:
	go build -o $(BINARY) ./cmd/cx

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
