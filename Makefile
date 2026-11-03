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

# Build the project site into public/. The base URL defaults to the published
# project page; override it with BASE_URL for a local build.
HUGO ?= hugo
BASE_URL ?= https://manu343726.github.io/hammer/

.PHONY: site site-serve
site:
	$(HUGO) --source site --destination "$(CURDIR)/public" --baseURL "$(BASE_URL)" --gc --minify

site-serve:
	$(HUGO) server --source site --baseURL "$(BASE_URL)"
