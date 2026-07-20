APP        := vacationplanner
PKG        := ./...
IMAGE      ?= ghcr.io/daknoblo/vacationplanner
TAG        ?= dev
PLATFORMS  ?= linux/amd64,linux/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: run
run: ## Run the server locally
	go run ./cmd/server

.PHONY: build
build: ## Build a static binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/$(APP) ./cmd/server

.PHONY: test
test: ## Run tests with the race detector and coverage
	go test -race -covermode=atomic -coverprofile=coverage.out $(PKG)

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint (must be installed)
	golangci-lint run

.PHONY: sec
sec: ## Run gosec static security scanner (must be installed)
	gosec -quiet $(PKG)

.PHONY: vuln
vuln: ## Run govulncheck (must be installed)
	govulncheck $(PKG)

.PHONY: docker-build
docker-build: ## Build a single-arch image for local use
	docker build -t $(IMAGE):$(TAG) .

.PHONY: docker-buildx
docker-buildx: ## Build a multi-arch image (requires buildx + QEMU)
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE):$(TAG) .

.PHONY: up
up: ## Start the app with docker compose
	docker compose up --build

.PHONY: down
down: ## Stop the stack and remove volumes
	docker compose down -v
