.PHONY: build test lint web-build docker-build docker-push

IMAGE ?= agent-orchestrator

build:
	CGO_ENABLED=0 go build -o bin/agent-orchestrator ./cmd/

test:
	go test ./...

lint:
	golangci-lint run ./...

web-build:
	cd web && npm ci && npm run build

docker-build:
	docker build -t $(IMAGE) .

docker-push:
	docker push $(IMAGE)
