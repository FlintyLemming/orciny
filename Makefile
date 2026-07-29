MODULE  := github.com/FlintyLemming/orciny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE).Version=$(VERSION)

.PHONY: all build build-hub build-agent build-web dev test lint clean

all: build

## build-web: 编译前端，产物落进 hub/internal/site/dist/ 供 //go:embed 内嵌
build-web:
	cd hub/internal/site && npm ci && npm run build

build-hub:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny ./cmd/orciny

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny-agent ./cmd/orciny-agent

build: build-web build-hub build-agent

## dev: 同时起 Vite 与 hub（dev tag，前端请求反代给 Vite）
dev:
	@echo "前端: http://127.0.0.1:5173   hub: http://127.0.0.1:8090"
	@trap 'kill 0' EXIT; \
	(cd hub/internal/site && npm run dev) & \
	go run -tags dev ./cmd/orciny serve --http=127.0.0.1:8090 --dir=./pb_data & \
	wait

test:
	go test -tags=testing ./...

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 未格式化: $$out"; exit 1; fi

clean:
	rm -rf dist
