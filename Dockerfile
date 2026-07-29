# syntax=docker/dockerfile:1
# PhonicsFun 容器镜像：三阶段构建（前端 → Go → 运行时）。
# 构建路径与 Makefile 保持一致：npm ci + vite build → go:embed → CGO_ENABLED=0 静态二进制。
#
# 单独构建：docker build -t phonicsfun .
# 多架构构建见 README「容器部署」：编译阶段全部钉在 $BUILDPLATFORM、
# 最终阶段零 RUN，跨架构构建不需要 QEMU/binfmt。
# 日常部署走 docker compose，见 docker-compose.yml 头部注释。

# —— 前端构建 ——
# 钉在构建机平台跑 node：前端产物与目标架构无关，交叉构建时省去模拟目标架构。
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# —— 后端构建 ——
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 覆盖 .dockerignore 掉的 web/dist（go:embed all:dist 的输入）
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/phonicsfun ./cmd/server

# —— 运行时 ——
# alpine 而非 scratch：healthcheck 需要 busybox wget。出站 TLS 的 CA 证书从
# 构建阶段拷贝（golang:alpine 自带 bundle，路径变了这里会构建期报错），
# 本阶段不跑 RUN——目标架构的层只有 COPY，跨架构构建无需 QEMU。
FROM alpine:3.22
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/phonicsfun /usr/local/bin/phonicsfun
ENV DATA_DIR=/data
VOLUME /data
EXPOSE 8080
# healthcheck 走 http://127.0.0.1，不受 HTTPS_PROXY 影响（busybox wget 只认小写 https_proxy）
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD wget -qO /dev/null "http://127.0.0.1:${PORT:-8080}/api/groups" || exit 1
ENTRYPOINT ["/usr/local/bin/phonicsfun"]
