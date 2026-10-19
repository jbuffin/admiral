.PHONY: build test lint e2e

build:
	npm --prefix web run build
	go build -o bin/admiral ./cmd/admiral

test:
	go test -count=1 $$(go list ./... | grep -v /e2e)
	npm --prefix web test

lint:
	go vet ./...
	npm --prefix web run typecheck

e2e: build
	go test -count=1 ./e2e/...
	npm --prefix web run e2e
