# PhonicsFun 自然拼读

教小孩英语自然拼读（phonics）的自托管 web 应用。家长录入单词（粘贴文本或拍照，AI 自动提取），AI 生成单词卡的全部内容——按词性组织的中英释义、例句、IPA 音标、"音节 → 字素-音素块"两级拼读拆解、整词发音音频、拼读音频（音节内逐块拼 → 合成音节 → 连读成词）——按"一次导入 = 一组"组织，学习时在组内循环翻卡。每组有单词列表页，可随时修正提取错误的词、增删单词、拖拽调序、改组名，改动的词自动重新生成。

- **AI 原生**：文本内容由 `gemini-3.5-flash-lite` 生成（结构化输出，内嵌 CMUdict 发音/音节数、Moby 音节切分、ECDICT 词性三路参照约束准确性）；音频由 `gemini-3.1-flash-live-preview` 的 Live API 生成（会话复用批量生成）。Google AI Studio 免费层即可运行。
- **点读与跟读高亮**：拼读音频带毫秒级时间标注（连续朗读经静音分割重组构造，非模型返回），播放时高亮当前音，点击任意拼读块/音节即可单独播放该段。
- **缓存优先**：所有内容在导入时后台预生成并落盘，翻卡零延迟；模型抽风时可对单卡分别重新生成文本/音频，重新生成文本时还可以附一句纠错反馈（如"音标不对，重音应在第一音节"），AI 会据此修正。
- **AI 老师**：点右下角浮动按钮开启与 AI 老师的实时语音对话（Gemini Live）。老师能带着学当前单词卡（介绍发音/意思/用法，引导跟读单词和例句并鼓励式纠音）、给当前单词组做听写（学生说"可以了"才读下一个）、或用简单英语自由聊天（老师会主动抛话题提问，孩子答不上来时先示范一句怎么答、再让孩子试一次）——三种玩法开口说即可切换，无需任何按钮。切换单词组/单词卡时上下文自动同步给老师，问"介绍一下这个单词"她就知道你看的是哪张卡。对话带双向实时字幕；设置页（首页右上角 ⚙️）可自定义老师性格、音色与"听说灵敏度"（老师判定孩子开口/说完的快慢，跟读短单词没反应或总被抢话时微调）。
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
| `TEACHER_MODEL` | 跟随 `LIVE_MODEL` | AI 老师对话模型 |
| `VOICE` | `Kore` | Live 预置音色（拼读发音；也是 AI 老师音色的默认值，老师音色可在设置页单独选择） |
| `RPM` | `12` | 出站请求限流（免费层按 15 RPM 留余量） |
| `HTTPS_PROXY` | — | 代理（REST 与 Live WebSocket 均经 Go 标准库透传） |

部署到软路由：`deploy/phonicsfun.service`（systemd）或 `deploy/phonicsfun.init`（OpenWrt procd），说明见文件头注释。

> **AI 老师需要 HTTPS（或 localhost）**：浏览器只在 secure context 下开放麦克风（`getUserMedia`），
> 用 `http://192.168.x.x` 访问时 AI 老师按钮会提示不可用（其余功能不受影响）。出路：
> ① 在软路由上加一层 HTTPS 反代（如 Caddy，一行配置自动签发内网证书）；
> ② 自签证书并在设备上安装信任（iOS 只有这两条路）；
> ③ 桌面 Chrome 可临时用 `chrome://flags` 的 `unsafely-treat-insecure-origin-as-secure` 白名单。

支持部署在反向代理的子路径下（前端资源相对路径 + hash 路由 + API 相对入口页解析，
后端无需任何配置）。要求反代**去掉前缀再转发**，并把无尾斜杠的入口重定向到带尾斜杠
（否则浏览器会把相对路径解析到根，资源与 API 全部 404）。Caddy 示例：

```
redir /phonicsfun /phonicsfun/ 301
handle_path /phonicsfun/* {
    reverse_proxy 192.168.1.1:8080
}
```

（`handle_path` 自带去前缀；AI 老师的 WebSocket 会被 `reverse_proxy` 自动代理，无需额外配置。）

容器部署：`docker compose up -d --build`（`Dockerfile` 多阶段构建，前端在镜像内现场编译）。
API key 与代理写在 compose 同目录的 `.env`（已 gitignore）里注入，数据落在 `./data`，
详见 `docker-compose.yml` 头部注释。

在开发机替软路由构建多架构镜像（编译阶段全部跑在构建机原生架构上，
最终阶段只有 COPY 层，因此跨架构构建**不需要** QEMU/binfmt）：

```sh
# 单架构：本机出 arm64 镜像，无 registry 时经 save/load 拷给软路由
docker buildx build --platform linux/arm64 -t phonicsfun --load .
docker save phonicsfun | ssh <路由器> docker load

# 多架构 manifest：一次出 amd64+arm64，推送到自己的 registry
# （经典镜像存储的 --load 装不下多架构清单，须 --push，
#   或在 Docker 设置里启用 containerd image store 后才能 --load）
docker buildx build --platform linux/amd64,linux/arm64 -t <registry>/phonicsfun --push .
```

## 数据布局

生成状态不落盘，**产物文件的存在性即完成态**——`card.json` 在即文本完成，两个 WAV 在即音频完成；重启后自动扫描补齐缺口，所有写入都是原子的（temp+fsync+rename）：

```
data/
├── words/<slug>/
│   ├── card.json         # 词性释义/例句/IPA/两级拆解（带 schema 版本号）
│   ├── word.wav          # 整词发音（慢速+常速各一遍）
│   ├── blend.wav         # 拼读：音节内逐块拼 → 合成音节 → 连读成整词
│   └── blend.cues.json   # blend 各段起止毫秒（点读/高亮用，与 blend.wav 成对生成）
├── groups/<id>.json      # 单词组（一次导入一组，跨组同词共享缓存）
└── settings.json         # 全局设置（AI 老师性格提示词/音色/听说灵敏度；缺失 = 全默认）
```

升级提示：card.json 带 schema 版本号，新版本启动时会自动检测旧版卡片并删除重建（全部文本+音频重新生成，消耗一轮 API 配额）。

## 开发

```sh
go test ./... -race            # 后端测试
go run ./cmd/spike -text       # Live API 链路独立验证（生成真实卡片并发音，需 GEMINI_API_KEY）
go run ./cmd/spike -teacher    # AI 老师链路独立验证（上下文注入 + 对话轮 + 转写，需 GEMINI_API_KEY）
cd web && npm run dev          # 前端热更新（/api 代理到 :8080 含 WebSocket，先把后端跑起来）
```

生成一组 20 词的完整内容约需 8-10 分钟（受免费层 RPM 限制），导入后即可先学已就绪的词，前端会轮询进度。

三份参照数据以 gzip 形式嵌入二进制，注入生成 prompt 约束模型输出：CMUdict（美音发音词典，见 `internal/llm/CMUDICT-LICENSE`）提供音素与音节数；Moby Hyphenator II（公有领域，见 `internal/llm/MOBY-LICENSE`）提供音节切分；ECDICT 裁剪子集（MIT，见 `internal/llm/ECDICT-LICENSE`）提供词性。后两份由 `tools/mkhyphdict`、`tools/mkposdict` 生成（产物已入库，工具留作可复现记录）。
