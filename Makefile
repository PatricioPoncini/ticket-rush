.PHONY: run test

run: ## start the demo UI on http://localhost:8080
	go run ./cmd/server

test:
	go vet ./...
	go test ./...
