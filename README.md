# PhonicsFun 自然拼读

教小孩英语自然拼读（phonics）的自托管 web 应用。家长录入单词（粘贴文本或拍照，AI 自动提取），AI 生成单词卡的全部内容——中英双语释义、IPA 音标、字素-音素拆解、整词发音音频、逐音素拼读音频——按"一次导入 = 一组"组织，学习时在组内循环翻卡。

- **AI 原生**：文本内容由 `gemini-3.5-flash-lite` 生成（结构化输出 + 内嵌 CMUdict 音素参照约束准确性）；音频由 `gemini-3.1-flash-live-preview` 的 Live API 生成（会话复用批量生成）。Google AI Studio 免费层即可运行。
- **缓存优先**：所有内容在导入时后台预生成并落盘，翻卡零延迟；模型抽风时可对单卡分别重新生成文本/音频。
- **面向软路由部署**：Go 单二进制（CGO_ENABLED=0），纯文件存储（无数据库），前端（Svelte 5）构建产物嵌入二进制，运行时零外部资源依赖。

## 构建

需要 Go 1.22+ 与 Node 20+（仅构建期需要 Node）：

```sh
make web        # 构建前端 → web/dist（会被 go:embed）
make release    # 交叉编译 bin/phonicsfun-linux-{amd64,arm64}
make test       # go vet + 全量测试（-race）
```

## 运行

唯一必填配置是 [Google AI Studio](https://aistudio.google.com/) 的 API key：

```sh
GEMINI_API_KEY=xxx ./bin/phonicsfun
# 打开 http://localhost:8080
```

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `GEMINI_API_KEY` | —（必填） | AI Studio API key |
| `PORT` | `8080` | 监听端口 |
| `DATA_DIR` | `./data` | 数据目录（组 JSON、卡片 JSON、WAV 音频） |
| `TEXT_MODEL` | `gemini-3.5-flash-lite` | 文本模型 |
| `LIVE_MODEL` | `gemini-3.1-flash-live-preview` | Live 音频模型（preview 模型改版时改这里即可） |
| `VOICE` | `Kore` | Live 预置音色 |
| `RPM` | `12` | 出站请求限流（免费层按 15 RPM 留余量） |
| `HTTPS_PROXY` | — | 代理（REST 与 Live WebSocket 均经 Go 标准库透传） |

部署到软路由：`deploy/phonicsfun.service`（systemd）或 `deploy/phonicsfun.init`（OpenWrt procd），说明见文件头注释。

## 数据布局

生成状态不落盘，**产物文件的存在性即完成态**——`card.json` 在即文本完成，两个 WAV 在即音频完成；重启后自动扫描补齐缺口，所有写入都是原子的（temp+fsync+rename）：

```
data/
├── words/<slug>/
│   ├── card.json    # 释义/IPA/拆解（前端渲染与拼读音频脚本的唯一数据源）
│   ├── word.wav     # 整词发音（慢速+常速各一遍）
│   └── blend.wav    # 逐音素拼读，最后读整词
└── groups/<id>.json # 单词组（一次导入一组，跨组同词共享缓存）
```

## 开发

```sh
go test ./... -race            # 后端测试
go run ./cmd/spike -text       # Live API 链路独立验证（生成真实卡片并发音，需 GEMINI_API_KEY）
cd web && npm run dev          # 前端热更新（/api 代理到 :8080，先把后端跑起来）
```

生成一组 20 词的完整内容约需 8-10 分钟（受免费层 RPM 限制），导入后即可先学已就绪的词，前端会轮询进度。

CMUdict（美音发音词典，BSD 许可，见 `internal/llm/CMUDICT-LICENSE`）以 gzip 形式嵌入二进制，为常见词的 IPA 转写提供权威参照。
