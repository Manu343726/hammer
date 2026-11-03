GO ?= go

.PHONY: all build test vet fmt tidy check

all: check

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

# What CI runs, and what a change has to pass before it is committed.
check: build vet test
