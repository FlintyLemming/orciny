MODULE  := github.com/FlintyLemming/orciny
# 与发版产物同一约定：去掉 v 前缀（0.3.0-37-g104876f）。hub 握手严格按 semver
# 解析 agent 版本，带 v 的会被当成「版本过旧」拒掉。找不到 tag（源码包、没拉 tag
# 的克隆）就不覆盖，沿用 orciny.go 里写的版本号——以前编进的 dev 与裸哈希都不是
# semver，agent 连不上 hub，install.sh 也下载不到。
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --dirty 2>/dev/null))
LDFLAGS := -s -w $(if $(VERSION),-X $(MODULE).Version=$(VERSION))

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
	cd hub/internal/site && npm test

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 未格式化: $$out"; exit 1; fi

clean:
	rm -rf dist
