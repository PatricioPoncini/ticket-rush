.PHONY: run sim test

run: ## start the demo UI on http://localhost:8080
	go run ./cmd/server

sim: ## run every scenario headless and write out/results.csv + charts
	go run ./cmd/sim

test:
	go vet ./...
	go test ./...
