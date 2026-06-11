.PHONY: up down logs sqlc lint test build tidy fmt agent-dist

COMPOSE := docker compose -f deploy/docker-compose.yml

# Cross-build the agent (pure-Go, static) for the common deploy targets into
# dist/. The agent is Windows-native (draft10); linux/* are for Docker/CI and
# the overlay e2e. Override targets with PLATFORMS="linux/amd64 ...".
PLATFORMS ?= linux/amd64 linux/arm64 windows/amd64
agent-dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  out=dist/yusui-agent-$$os-$$arch$$ext; \
	  echo "building $$out"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -C agent -trimpath -ldflags='-s -w' -o ../$$out ./cmd/yusui-agent; \
	done

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
