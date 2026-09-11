.PHONY: build check clean dev fmt install lint test

ARGS ?=

dev:
	go run . $(ARGS)

build:
	go build -o diffnote .

test:
	go test -race ./...

lint:
	go vet ./...
	node --check web/app.js

fmt:
	gofmt -w *.go internal/gitrepo/*.go internal/store/*.go

check: test lint

install:
	go install .

clean:
	rm -f diffnote coverage.out
