# PhonicsFun 构建入口
#
#   make web      构建前端（产物进 web/dist，被 go:embed）
#   make build    构建当前平台二进制到 bin/
#   make release  交叉编译软路由双目标（linux/amd64 + linux/arm64）
#   make test     全量测试（含 -race）
#   make run      本地起服务（需要 .secrets 提供 GEMINI_API_KEY）

GO_LDFLAGS := -s -w

.PHONY: all web build release test run clean

all: web release

web:
	cd web && npm install && npm run build
	@touch web/dist/.gitkeep  # vite emptyOutDir 会清掉占位文件，补回以保持 go:embed 与 git 状态稳定

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/phonicsfun ./cmd/server

release:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/phonicsfun-linux-amd64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/phonicsfun-linux-arm64 ./cmd/server
	@ls -lh bin/

test:
	go vet ./...
	go test ./... -count=1 -race

run: build
	@if [ -f .secrets ]; then set -a && . ./.secrets && set +a && ./bin/phonicsfun; \
	else ./bin/phonicsfun; fi

clean:
	rm -rf bin/ web/dist/* && touch web/dist/.gitkeep
