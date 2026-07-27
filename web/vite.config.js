import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// base: './' —— 产物用相对路径引用资源，Go go:embed 后可在任意路径前缀下服务。
export default defineConfig({
  base: './',
  plugins: [
    svelte(),
    {
      // 根 .gitignore 忽略 web/dist/* 但保留 web/dist/.gitkeep（让 go:embed 始终有目录可嵌）。
      // vite build 会清空 outDir，这里在每次构建结束后把 .gitkeep 补回来。
      name: 'restore-dist-gitkeep',
      closeBundle() {
        writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '');
      },
    },
  ],
  build: {
    outDir: 'dist',
    target: 'es2022',
  },
  server: {
    proxy: {
      // dev 时把 /api 转发给本地 Go 后端（后端默认 PORT=8080）
      '/api': 'http://localhost:8080',
    },
  },
});
