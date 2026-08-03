# AGENTS.md — 给 AI agent 的项目指南

PhonicsFun 是教小孩英语自然拼读的自托管 web 应用：家长录入单词（粘贴文本/拍照，AI 提取），AI 生成单词卡全部内容（中英释义、IPA、字素-音素拆解、整词发音音频、逐音素拼读音频），按"一次导入 = 一组"组织，学习时组内循环翻卡；组内单词可在列表页修改/增删/拖拽调序，改动的词自动重新生成。另有"AI 老师"实时语音对话（Gemini Live，浏览器麦克风经后端 WS 代理双向流），可引导学单词/听写/自由英语聊天，切组切卡时前端向常驻连接注入上下文便签。Go 单二进制 + 纯文件存储 + 内嵌 Svelte 5 前端，部署目标是资源拮据的软路由。产品需求原文见 `IDEA.md`，面向人类的说明见 `README.md`。

## 常用命令

```sh
make test                # go vet + 全量测试（-race）——任何 Go 改动后必跑
make web                 # 构建前端 → web/dist（被 go:embed，改前端后需重跑才会进二进制）
make build               # 当前平台二进制 → bin/phonicsfun
make release             # 交叉编译 linux/amd64 + linux/arm64
make run                 # 本地起服务（自动 source .secrets），http://localhost:8080
go run ./cmd/spike -text # AI 链路独立验证：真实生成一张卡并发音（需 API key）
go run ./cmd/spike -teacher # AI 老师链路独立验证：便签注入 + 对话轮 + 转写（需 API key）
```

## 密钥约定（重要）

仓库根目录的 `.secrets` 文件（内容 `export GEMINI_API_KEY=...`）由用户手工放置：

- **禁止读取其内容，禁止提交**（已在 `.gitignore`）。
- 需要真实 API 时：`set -a && source .secrets && set +a && <命令>`。

## 架构地图

```
cmd/server/          入口：config → store → llm → pipeline → httpapi，优雅退出
cmd/spike/           Live/文本链路独立验证程序（复用 llm/pipeline 同一套代码路径，
                     含 blend 分割重组；-text 走真实文本生成；-teacher 验证 AI 老师
                     对话链路：[CONTEXT] 注入 + 文本轮 + 双向转写）
internal/config/     env 解析（见下方环境变量表）
internal/store/      文件存储层：Slug 归一化、原子写、Card(v2)/Group/Cues 类型与 CRUD、
                     Settings（settings.json：AI 老师提示词/音色/VAD 灵敏度覆盖值）
internal/llm/        Gemini 封装：GenerateCard / ExtractWords(FromImage) / Live 会话（TTS）/
                     TeacherSession（AI 老师双工对话：音频入出、转写、resumption）
                     内嵌三份参照数据注入 prompt（均为 gzip + 行偏移二分模式）：
                     CMUdict（发音+音节数）、mhyph.tsv.gz（Moby 音节切分，见
                     MOBY-LICENSE）、posdict.tsv.gz（ECDICT 词性，见 ECDICT-LICENSE）
internal/pipeline/   生成流水线：text/audio 两条串行 worker + 优先级队列 + 退避重试
                     BlendAudio：blend 连续朗读 → 静音分割 → 重组 + cues 构造
internal/wav/        PCM → WAV（44 字节头）+ 静音处理（SplitBySilence/TrimSilence/Silence）
internal/httpapi/    REST API + 内嵌 SPA 服务（含 Range 音频、cues JSON）
                     teacher.go：AI 老师 WS 桥接（浏览器 PCM ↔ Gemini Live，全局单路）
tools/mkposdict/     一次性数据生成工具（产物 .gz 已入库，工具留作可复现记录）
tools/mkhyphdict/
web/                 Svelte 5 + Vite 前端；约定见 web/README.md
web/embed.go         //go:embed all:dist；dist/.gitkeep 保证未构建时也能编译
```

环境变量：`GEMINI_API_KEY`（必填）、`PORT=8080`、`DATA_DIR=./data`、`TEXT_MODEL=gemini-3.5-flash-lite`、`LIVE_MODEL=gemini-3.1-flash-live-preview`、`TEACHER_MODEL`（AI 老师对话模型，默认跟随 `LIVE_MODEL`）、`VOICE=Kore`（拼读 TTS 音色，也是老师音色的默认值；老师音色可在设置页单独覆盖，存 settings.json）、`RPM=12`、`HTTPS_PROXY`（标准库透传）。

## 核心不变量（改代码前必读）

1. **产物文件存在性即完成态**。`data/words/<slug>/card.json` 在 = 文本完成，`word.wav`+`blend.wav` 在 = 音频完成。`blend.cues.json`（时间标注）不参与完成态判定，但生成顺序保证"blend.wav 在则 cues 必在"（先写 cues 后写 wav）。没有状态文件；pending/running/failed 只存在于 pipeline 内存（进程重启即丢，`Pipeline.Recover()` 扫描补缺口）。任何引入"第二事实源"的改动都会破坏崩溃自洽性。
2. **所有落盘走原子写**（`store.writeFileAtomic`：temp + fsync + rename）。磁盘上不允许出现半成品文件。
3. **重新生成（force）= 先删产物再入队**（`Pipeline.Regenerate`），job 本身不带 force 标志，worker 只看文件是否存在。text 重生成会连带删音频（拼读脚本依赖新文本），这个级联不能去掉；`DeleteAudio` 连带删 cues。重新生成可附用户纠错反馈（≤500 字），只注入文本生成 prompt（`GenerateCard` 的 `<feedback>` 段）——音频脚本机械拼装、不接受自由文本（见不变量 5），target=audio 时反馈被忽略。反馈存 pipeline 内存（`slug → feedback`，worker 处理时消费，最后一次反馈赢）而非 job/磁盘：与不变量 1 同理，不落盘就不会成为第二事实源，重启后 Recover 补缺口时无反馈是有意为之。
4. **card.json 是带版本的两级结构**（`schema: 2`，音节 → 音节内 chunk）。硬不变量（`store.Card.Validate`，llm 生成时校验、前端渲染时复验）：音节 text 依序拼接 == 小写 word；音节内 chunk 的 grapheme 依序拼接 == 音节 text（不发音字母单独成 chunk 标 `silent:true`）；非 silent chunk 必有 `respell` + `anchor_word`，音节必有 `respell`；多 voiced chunk 的音节里 chunk.respell 不得等于 syllable.respell（那是"把整音节读音塞给单个 chunk"，会被逐块朗读）；senses/examples 非空且词性在标准缩写枚举内；digraph 不可拆开、blend 不可合并（机械黑白名单）。**卡内只允许一种发音**：非 silent chunk 的 phoneme 依序拼接必须与整词 ipa 是同一读音（`llm.checkIPAConsistency`，重音符/音节点/常见同音异写折叠、双写辅音压缩后比较——hello 的两个 l 各成 chunk 各标 /l/，整词 ipa 只有一个 l，属正常）；respell 的弱读元音拼法必须跟随 phoneme（`llm.checkRespellConsistency`，窄校验 ə/ɪ 这对易混点：/ɪ/ 音节的 respell 不得写成 schwa 拼法 "uh"，反之不得写 "ih"——音节段朗读用 syllable.respell，混写=读错音）。弱读元音质量的取舍在 prompt：字母 i 的弱读按教学词典标 /ɪ/（保留字母与读音的关联），-tion/-il 类仍是 /ə/。这两个校验只在 llm 生成时把关，不在 `Validate` 里——它们带风格容差与启发式，且存量卡不应因收紧而在前端报警。**改 schema 必须递增 `store.CardSchemaVersion`**——`Recover()` 发现旧版卡会自动删产物重建（升级即全量重生成，耗一轮配额）。
5. **音频 prompt 永不含裸 IPA，也不含裸拼写的音节**（Live 模型会把 tion 读成 tee-on）。拼读条目由 `llm.BuildBlendLines` 机械拼装：chunk 行用 chunk.respell（spelling voice，不弱读），音节行/连读用 syllable.respell（真实读音，含 schwa）。改拼读发音应改 respell/anchor_word 的生成质量，而不是往脚本里塞 IPA。
6. **blend 音频与时间标注由"分割重组"构造**（`pipeline.BlendAudio`）：主体轮一次连续朗读全部条目（模型条目间停顿≈1s 是静音分割的生命线，systemInstruction 与脚本里的停顿指令不能删），按已知条目数 `wav.SplitBySilence` 切段（段数不符 = 朗读失控，自动重试），修剪后按固定静音间隔重组。**收尾段本地拼装，不占朗读轮次**：素材全部取自已落盘 word.wav 按静音切出的慢速/常速两遍。多音节词 = 慢速遍 + 常速遍——"音节串读"由慢速遍承担（模型对整词的自然慢读，音节清晰、衔接连贯；不要改回用主体轮的孤立音节段快放拼接，那会丢协同发音与连贯语调，听感机械、和整词读音脱节）；单音节词只有慢速遍。拼读收尾、点大字区、整词按钮播的是同一份录音，听感不会互相漂移，这个同源性不能破坏。word 轮脚本要求两遍之间整秒静音，落盘前硬校验能切出两段；存量 word.wav 切不出两遍时自动删除重生（`errWordAudioUnusable`）。tail 段内的整词部分另标 `word` 子区间 cue（点大字区单独播一遍完整读音），它与 tail 重叠且必须排在 tail 之后——前端整段播放的命中逻辑靠这个顺序。**拼读节奏调 pipeline 的 gap*/tailWordGap 常量，不要去调 prompt**；cues 毫秒即重组时的字节偏移，改重组逻辑必须同步保证 cues 精确。
7. **Live 会话复用**：每会话最多 8 词、13 分钟，收到 GoAway 或出错即轮换（常量在 `internal/llm/live.go`）；音频会话官方上限约 15 分钟，轮换阈值必须留余量。单词重生成走全新小会话。每词 2 轮且顺序固定：先整词（blend 收尾依赖 word.wav），后 blend 主体。AI 老师对话会话（`internal/llm/teacher.go`）同受 13 分钟 + GoAway 轮换约束，轮换只在轮次边界做、靠非透明 SessionResumption handle 续对话历史（handle 失效自动回落全新会话 + 重注入上下文便签）；桥接层强制全局单路（新连接 takeover 踢旧），与 pipeline 的 1 路合计 2 路，留免费层（约 3 路并发）的轮换交叠余量。
8. **所有出站 Gemini 调用（含 Live 建会话、AI 老师建会话/轮换重连）共享 `llm.Client` 里的一个 rate.Limiter**。免费层限额官方不再公布，按 15 RPM / 1000 RPD 保守假设（默认 12 RPM 留余量）；Live 会话内的轮次不计请求数。
9. **slug**（`store.Slug`）：小写、仅 `[a-z0-9']`、`'`→`_`。同 slug 即同词，跨组共享缓存目录；URL 里的 slug 参数一律先过 `store.Slug` 再拼路径（防穿越）。
10. **AI 老师的提示词与 [CONTEXT] 便签是一对契约**：老师会话的 systemInstruction 是 `llm.teacherBaseInstruction` + 家长自定义段（`BuildTeacherInstruction`），与 TTS 用的 `liveSystemInstruction` 完全独立、互不影响。`[CONTEXT]` 便签（`llm.ContextNote*`）与基础指令的 CONTEXT NOTES 段必须同步改动；便签用 `SendClientContent(turnComplete=false)` 注入（只累积进 prompt、不触发回应），模型说话中到达的便签由桥接排队到轮次边界（TurnComplete/Interrupted）再注入。改动后跑 `go run ./cmd/spike -teacher` 验证便签不被朗读、老师"知道"当前卡。老师"没听见短促跟读/接话慢/抢话"属于 VAD 端点检测问题：调 `llm.teacherVAD*` 默认常量或引导用户去设置页调"听说灵敏度"（settings 覆盖值），别去动提示词。

## HTTP API 契约

改任何一行都要同步 `web/src/lib/api.js` 和本表。

| 方法/路径 | 说明 |
|---|---|
| `POST /api/extract` | JSON `{text}` 或 multipart `image` → `{words:[...]}` |
| `POST /api/groups` | `{name?, words:[]}` → `{id}`；创建即全量入队 |
| `GET /api/groups` | `[{id,name,createdAt,total,ready}]` |
| `GET /api/groups/{id}` | `{..., words:[{word,slug,text,audio,audioVersion?,error?}]}`，状态值 `pending/running/done/failed`，前端轮询；`audioVersion` 是音频产物 mtime，前端音频/cues URL 的 `?v=` 防缓存参数（audio-only 重生成时 card 的 generated_at 不变，不能用它） |
| `PUT /api/groups/{id}` | `{name?, words:[]}` → `{id}`；全量替换组名与词表（顺序即翻卡顺序），词表全量重新入队（缺产物的自动生成），被移除且不再被任何组引用的词连带删除产物 |
| `DELETE /api/groups/{id}` | `?purge=1` 连带删除无其他组引用的词目录 |
| `GET /api/words/{slug}` | card.json（v2：schema/senses/examples/syllables） |
| `GET /api/words/{slug}/audio/{word\|blend}.wav` | 音频，支持 Range（iOS Safari 必需） |
| `GET /api/words/{slug}/audio/blend.cues.json` | blend 时间标注（点读/高亮）；404 = 无 cues，前端降级 |
| `POST /api/words/{slug}/regenerate` | `{target:"text"\|"audio"\|"both", feedback?}` → 202；生成中返回 409；`feedback` 是用户纠错意见（≤500 字，超长 400），注入文本生成 prompt，target=audio 时忽略 |
| `GET /api/settings` | `{teacherPrompt, teacherVoice, teacherVadPrefixMs, teacherVadSilenceMs}`；文件缺失返回零值默认（VAD 两值 0 = 跟随 `llm` 内置默认） |
| `PUT /api/settings` | 同上结构 → 200 回显；`teacherPrompt` >2000 字 → 400；VAD 两值非 0 时须在 `store.Min/MaxTeacherVAD*Ms` 区间内（20-500 / 200-2000 毫秒），否则 400；改动下次开启 AI 老师生效 |
| `GET /api/teacher/live` | **WebSocket**（AI 老师实时语音）。二进制帧 = 裸 PCM16 LE mono：浏览器→服务 16kHz 麦克风、服务→浏览器 24kHz 老师语音。文本帧 = JSON：↑`context`（组词表）/`card`（当前卡）/`mic`（on=false 暂停采集）；↓`ready`/`transcript`（role=user\|teacher 增量）/`interrupted`/`turn_complete`/`restarted`/`error`（fatal 后关连接）。全局单路，新连接踢旧连接 |

缓存策略（httpapi 顶部注释是权威）：动态 JSON 一律 `no-store`；card.json `no-cache`（URL 无版本参数，靠 Last-Modified revalidate）；音频/cues 带 `?v=` 时 `immutable` 永久缓存、裸 URL 退 `no-cache`；SPA 的 `assets/`（文件名带 hash）`immutable`，入口 `no-cache`。新增文件类端点必须显式声明缓存头——`http.ServeFile` 默认带 Last-Modified 无 Cache-Control，会被浏览器启发式缓存，产物重新生成后普通刷新看不到新内容。

## 环境与已知坑

- **本 devcontainer 里 8001 端口被 localhost-proxy 占用**，本地一律用 8080（后端默认、vite proxy、文档均已统一）。
- `gemini-3.1-flash-live-preview` 是 preview 模型，改版/下线时改 `LIVE_MODEL` env 即可，不要写死新模型名到代码里。
- 前端产物必须零外链（无 CDN/webfont），`base:'./'` 相对路径；构建后 vite 插件会补回 `web/dist/.gitkeep`，别删这个机制。
- 测试里不要依赖真实 API：pipeline 通过 `pipeline.LLM`/`AudioSession` 接口注入 fake（见 `pipeline_test.go`）；老师桥接通过 `httpapi.teacherConn`/`teacherDialer` 注入 fake（见 `teacher_test.go`）。
- **AI 老师的麦克风需要 secure context**：`getUserMedia` 在 `http://192.168.x.x` 这类局域网明文地址下不存在（HTTPS 或 localhost 才有）。前端已检测并提示；部署侧的出路（反代 HTTPS/自签证书）写在 README。
- `http.Server` 故意不设读写超时（`cmd/server/main.go`）——`WriteTimeout` 会杀死老师的 WS 长连接；若将来要加超时，WS 路径必须用 `ResponseController` 清 deadline 或单独豁免。进程退出时 WS 由 `RegisterOnShutdown(api.CloseTeacher)` 主动断开（`Shutdown` 不管 hijack 的连接）。

## 改动后的验证清单

1. 任何 Go 改动：`make test`（vet + 全量 -race）。
2. 触碰 AI 链路（prompt、llm、live）：`set -a && source .secrets && set +a && go run ./cmd/spike -text`，人耳试听输出的 WAV；触碰 AI 老师链路（teacher prompt/便签/会话）另跑 `go run ./cmd/spike -teacher`，核对转写与试听。
3. 触碰前端：`cd web && npm run build`，确认产物无 `http(s)://` 外链；改了 embed 相关则重跑 `make build` 验证。
4. 触碰 API 契约或行为：同步更新 `web/src/lib/api.js`、本文件的 API 表、`README.md`。
5. 端到端（需 key）：`make run` → 导入几个词 → 轮询到全就绪 → 页面试听 → 触发一次 regenerate。
