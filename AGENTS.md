# AGENTS.md — 给 AI agent 的项目指南

PhonicsFun 是教小孩英语自然拼读的自托管 web 应用：家长录入单词（粘贴文本/拍照，AI 提取），AI 生成单词卡全部内容（中英释义、IPA、字素-音素拆解、整词发音音频、逐音素拼读音频），按"一次导入 = 一组"组织，学习时组内循环翻卡。Go 单二进制 + 纯文件存储 + 内嵌 Svelte 5 前端，部署目标是资源拮据的软路由。产品需求原文见 `IDEA.md`，面向人类的说明见 `README.md`。

## 常用命令

```sh
make test                # go vet + 全量测试（-race）——任何 Go 改动后必跑
make web                 # 构建前端 → web/dist（被 go:embed，改前端后需重跑才会进二进制）
make build               # 当前平台二进制 → bin/phonicsfun
make release             # 交叉编译 linux/amd64 + linux/arm64
make run                 # 本地起服务（自动 source .secrets），http://localhost:8080
go run ./cmd/spike -text # AI 链路独立验证：真实生成一张卡并发音（需 API key）
```

## 密钥约定（重要）

仓库根目录的 `.secrets` 文件（内容 `export GEMINI_API_KEY=...`）由用户手工放置：

- **禁止读取其内容，禁止提交**（已在 `.gitignore`）。
- 需要真实 API 时：`set -a && source .secrets && set +a && <命令>`。

## 架构地图

```
cmd/server/          入口：config → store → llm → pipeline → httpapi，优雅退出
cmd/spike/           Live/文本链路独立验证程序（复用 llm 包同一套代码路径）
internal/config/     env 解析（见下方环境变量表）
internal/store/      文件存储层：Slug 归一化、原子写、Card/Group 类型与 CRUD
internal/llm/        Gemini 封装：GenerateCard / ExtractWords(FromImage) / Live 会话
                     内嵌 CMUdict（cmudict.dict.gz，二分查找，注入 prompt 作发音参照）
internal/pipeline/   生成流水线：text/audio 两条串行 worker + 优先级队列 + 退避重试
internal/wav/        PCM → WAV（44 字节头，无外部依赖）
internal/httpapi/    REST API + 内嵌 SPA 服务（含 Range 音频）
web/                 Svelte 5 + Vite 前端；约定见 web/README.md
web/embed.go         //go:embed all:dist；dist/.gitkeep 保证未构建时也能编译
```

环境变量：`GEMINI_API_KEY`（必填）、`PORT=8080`、`DATA_DIR=./data`、`TEXT_MODEL=gemini-3.5-flash-lite`、`LIVE_MODEL=gemini-3.1-flash-live-preview`、`VOICE=Kore`、`RPM=12`、`HTTPS_PROXY`（标准库透传）。

## 核心不变量（改代码前必读）

1. **产物文件存在性即完成态**。`data/words/<slug>/card.json` 在 = 文本完成，`word.wav`+`blend.wav` 在 = 音频完成。没有状态文件；pending/running/failed 只存在于 pipeline 内存（进程重启即丢，`Pipeline.Recover()` 扫描补缺口）。任何引入"第二事实源"的改动都会破坏崩溃自洽性。
2. **所有落盘走原子写**（`store.writeFileAtomic`：temp + fsync + rename）。磁盘上不允许出现半成品文件。
3. **重新生成（force）= 先删产物再入队**（`Pipeline.Regenerate`），job 本身不带 force 标志，worker 只看文件是否存在。text 重生成会连带删音频（拼读脚本依赖新文本），这个级联不能去掉。
4. **card.json 两条硬不变量**（`store.Card.Validate`，llm 生成时校验、前端渲染时复验）：chunks 的 grapheme 依序拼接精确等于小写 word（不发音字母单独成 chunk 标 `silent:true`）；非 silent chunk 必有 `respell` + `anchor_word`。
5. **音频 prompt 永不含裸 IPA**（Live 模型会读错）。拼读脚本由 `llm.BuildBlendScript` 从 chunks 的 respell+anchor_word 机械拼装，改拼读发音行为应改 respell/anchor_word 的生成质量，而不是往脚本里塞 IPA。
6. **Live 会话复用**：每会话最多 8 词、13 分钟，收到 GoAway 或出错即轮换（常量在 `internal/llm/live.go`）；音频会话官方上限约 15 分钟，轮换阈值必须留余量。单词重生成走全新小会话。
7. **所有出站 Gemini 调用（含 Live 建会话）共享 `llm.Client` 里的一个 rate.Limiter**。免费层限额官方不再公布，按 15 RPM / 1000 RPD 保守假设（默认 12 RPM 留余量）；Live 会话内的轮次不计请求数。
8. **slug**（`store.Slug`）：小写、仅 `[a-z0-9']`、`'`→`_`。同 slug 即同词，跨组共享缓存目录；URL 里的 slug 参数一律先过 `store.Slug` 再拼路径（防穿越）。

## HTTP API 契约

改任何一行都要同步 `web/src/lib/api.js` 和本表。

| 方法/路径 | 说明 |
|---|---|
| `POST /api/extract` | JSON `{text}` 或 multipart `image` → `{words:[...]}` |
| `POST /api/groups` | `{name?, words:[]}` → `{id}`；创建即全量入队 |
| `GET /api/groups` | `[{id,name,createdAt,total,ready}]` |
| `GET /api/groups/{id}` | `{..., words:[{word,slug,text,audio,error?}]}`，状态值 `pending/running/done/failed`，前端轮询 |
| `DELETE /api/groups/{id}` | `?purge=1` 连带删除无其他组引用的词目录 |
| `GET /api/words/{slug}` | card.json |
| `GET /api/words/{slug}/audio/{word\|blend}.wav` | 音频，支持 Range（iOS Safari 必需） |
| `POST /api/words/{slug}/regenerate` | `{target:"text"\|"audio"\|"both"}` → 202；生成中返回 409 |

## 环境与已知坑

- **本 devcontainer 里 8001 端口被 localhost-proxy 占用**，本地一律用 8080（后端默认、vite proxy、文档均已统一）。
- `gemini-3.1-flash-live-preview` 是 preview 模型，改版/下线时改 `LIVE_MODEL` env 即可，不要写死新模型名到代码里。
- 前端产物必须零外链（无 CDN/webfont），`base:'./'` 相对路径；构建后 vite 插件会补回 `web/dist/.gitkeep`，别删这个机制。
- 测试里不要依赖真实 API：pipeline 通过 `pipeline.LLM`/`AudioSession` 接口注入 fake（见 `pipeline_test.go`）。

## 改动后的验证清单

1. 任何 Go 改动：`make test`（vet + 全量 -race）。
2. 触碰 AI 链路（prompt、llm、live）：`set -a && source .secrets && set +a && go run ./cmd/spike -text`，人耳试听输出的 WAV。
3. 触碰前端：`cd web && npm run build`，确认产物无 `http(s)://` 外链；改了 embed 相关则重跑 `make build` 验证。
4. 触碰 API 契约或行为：同步更新 `web/src/lib/api.js`、本文件的 API 表、`README.md`。
5. 端到端（需 key）：`make run` → 导入几个词 → 轮询到全就绪 → 页面试听 → 触发一次 regenerate。
