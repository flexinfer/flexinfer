.PHONY: all build docker-build docker-push deploy test lint docs

# Build all binaries
build:
	go build -o bin/flexinfer-agent ./cmd/flexinfer-agent
	go build -o bin/flexinfer-bench ./cmd/flexinfer-bench
	go build -o bin/flexinfer-manager ./cmd/flexinfer-manager
	go build -o bin/flexinfer-sched ./cmd/flexinfer-sched

# Build and push the docker image
docker-build:
	docker build -t $(IMG) .

docker-push:
	docker push $(IMG)

# Deploy to kind
deploy:
	make docker-build docker-push IMG=harbor.lan/library/flexinfer:dev
	kind load docker-image harbor.lan/library/flexinfer:dev
	make deploy

# Run tests
test:
	go test ./...

# Lint
lint:
	golangci-lint run

# Generate docs
docs:
	/home/cblevins/go/bin/gomarkdoc --output docs/reference.md ./api/... ./cmd/... ./controllers/... ./agents/... ./pkg/... ./scheduler/...
