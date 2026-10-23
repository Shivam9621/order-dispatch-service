.PHONY: run test lint up

run:
	go run cmd/server/main.go

test:
	go test -race -v ./...

lint:
	go vet ./...

up:
	docker compose up -d
