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
