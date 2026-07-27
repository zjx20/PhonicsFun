# PhonicsFun 前端（web/）

「PhonicsFun 自然拼读」的前端：教中国小孩英语自然拼读的单人家用 web 应用。
Svelte 5 + Vite 构建，产物由 Go 后端 `go:embed` 后与 API 同源提供服务。

## 开发

```bash
cd web
npm install
npm run dev        # http://localhost:5173
```

dev 服务器把 `/api` 代理到 `http://localhost:8080`（见 `vite.config.js`），
所以本地开发需要 Go 后端先在 8080 端口跑起来。

## 构建

```bash
npm run build      # 产物输出到 web/dist
```

- `base: './'`：所有资源用相对路径引用，嵌入 Go 后可挂在任意路径前缀下。
- `build.target: 'es2022'`。
- 根 `.gitignore` 忽略 `web/dist/*` 但保留 `web/dist/.gitkeep`；
  构建插件会在每次 build 后补回 `.gitkeep`。

## 约定

- **Svelte 5 runes**：组件内用 `$state / $derived / $effect / $props`；
  全局状态放在 `src/lib/*.svelte.js` 模块里导出 `$state` 对象（不用旧版 `svelte/store`）。
- **手写 hash 路由**（无路由库，见 `src/App.svelte`）：
  `#/` 首页、`#/import` 导入向导、`#/group/{id}` 播放页。
- **运行时零外部资源**：无 CDN / webfont / 外部图片，图标用 emoji 或内联 SVG，字体走系统字体栈。
- **API**：全部相对路径 `/api/...`，封装在 `src/lib/api.js`，错误统一抛 `Error(中文消息)`，
  由调用方弹 Toast（`src/lib/toast.svelte.js`）。
- **UI 文案全部中文（zh-CN）**，移动端优先，触控目标 ≥44px。

## 目录

```
src/App.svelte                 hash 路由分发 + 全局 Toast
src/pages/HomePage.svelte      组列表（未全就绪时每 5s 轮询）
src/pages/ImportWizard.svelte  导入向导：input → extracting → pick → creating
src/pages/PlayerPage.svelte    组详情即播放页（滑动/按钮翻卡，未就绪每 2s 轮询）
src/components/               GroupCard / WordChip / WordCard / ChunkRow /
                               PlayButtons / CardMenu / CardPlaceholder / Toast
src/lib/api.js                 fetch 封装（后端 API 契约的唯一入口）
src/lib/groups.svelte.js       组列表状态 + 轮询控制
src/lib/player.svelte.js       播放页状态：当前组/卡索引、卡片缓存、重新生成跟踪
src/lib/toast.svelte.js        全局 Toast
src/lib/audio.js               原生 Audio 播放 + 相邻卡预加载
```
