.PHONY: build check clean dev fmt install lint test test-web uninstall

ARGS ?=
BINDIR ?= $(HOME)/.local/bin

dev:
	go run . $(ARGS)

build:
	go build -o diffnote .

test:
	go test -race ./...

test-web:
	npm test

lint:
	go vet ./...
	node --check web/app.js

fmt:
	gofmt -w *.go internal/gitrepo/*.go internal/store/*.go

check: test test-web lint

install: build
	install -d "$(BINDIR)"
	install -m 0755 diffnote "$(BINDIR)/diffnote"

uninstall:
	rm -f "$(BINDIR)/diffnote"

clean:
	rm -f diffnote coverage.out
