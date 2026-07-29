MODULE  := github.com/FlintyLemming/orciny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE).Version=$(VERSION)

.PHONY: all build build-hub build-agent build-web test lint clean

all: build

## build-web: 编译前端（计划 8 接入 site/ 之后才可用）
build-web:
	cd hub/internal/site && npm ci && npm run build

build-hub:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny ./cmd/orciny

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny-agent ./cmd/orciny-agent

build: build-hub build-agent

test:
	go test -tags=testing ./...

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 未格式化: $$out"; exit 1; fi

clean:
	rm -rf dist
