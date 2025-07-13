.PHONY: all docker-build docker-push deploy test lint docs

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
