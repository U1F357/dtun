GO ?= go
.PHONY: build test vet check
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o dtun ./cmd/dtun
test:
	$(GO) test -race ./...
vet:
	GOOS=linux GOARCH=amd64 $(GO) vet ./...
check: test vet
