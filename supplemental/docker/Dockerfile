# 三段：Node 编前端 → Go 编二进制（把 dist 一起 embed）→ alpine 运行时。
# 前端产物必须在 go build 之前就位，否则 //go:embed all:dist 拿到的是占位页。

FROM node:24-alpine AS web
WORKDIR /src/hub/internal/site
COPY hub/internal/site/package.json hub/internal/site/package-lock.json ./
RUN npm ci
COPY hub/internal/site/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/hub/internal/site/dist ./hub/internal/site/dist
# 不传 VERSION 就不覆盖，沿用 orciny.go 里写的版本号。以前默认编进 dev：
# 没有哪个 release 叫 dev，GET /install.sh 拿它去下载必然 404。
ARG VERSION
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w ${VERSION:+-X github.com/FlintyLemming/orciny.Version=${VERSION}}" \
      -o /out/orciny ./cmd/orciny

FROM alpine:3.22
# ca-certificates：hub 要以 HTTPS 访问外部（M2 的 fetch 型采集器）
# tzdata：日志与事件时间戳按部署地时区显示
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/orciny /usr/local/bin/orciny
VOLUME /pb_data
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/orciny"]
CMD ["serve", "--http=0.0.0.0:8090", "--dir=/pb_data"]
