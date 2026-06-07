.PHONY: up down logs sqlc lint test build tidy fmt

COMPOSE := docker compose -f deploy/docker-compose.yml

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down -v

logs:
	$(COMPOSE) logs -f --tail=100

sqlc:
	sqlc generate -f server/internal/store/sqlc.yaml

lint:
	cd server && golangci-lint run ./...
	cd agent && golangci-lint run ./...

test:
	cd server && go test ./...
	cd agent && go test ./...

build:
	go build github.com/catundercar/yusui/server/... github.com/catundercar/yusui/agent/...

tidy:
	cd server && go mod tidy
	cd agent && go mod tidy

fmt:
	gofmt -w server agent
