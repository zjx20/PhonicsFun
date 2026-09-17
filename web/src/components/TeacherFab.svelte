<script>
  // AI 老师全局浮动按钮 + 对话面板。挂在 App.svelte，跨路由常驻——
  // WS 连接不随页面切换重建。上下文注入（当前组/当前卡 → 老师会话）的
  // 两个 $effect 也放在这里：本组件常驻，等价于模块级 effect。
  import { untrack } from 'svelte';
  import {
    teacherState,
    startTeacher,
    stopTeacher,
    syncGroup,
    syncCard,
    sendPhoto,
  } from '../lib/teacher.svelte.js';
  import { playerState } from '../lib/player.svelte.js';

  let open = $state(false);

  const active = $derived(teacherState.status !== 'idle' && teacherState.status !== 'error');
  const statusText = $derived(
    {
      connecting: '连接中…',
      listening: '在听，说话吧',
      speaking: '老师说话中',
      error: teacherState.error || '出错了',
    }[teacherState.status] ?? ''
  );

  // 切组注入：只依赖 group.id 的值；2s 轮询替换 group 引用时 effect 会
  // 重跑，但 syncGroup 按 id 去重，不会重发。
  $effect(() => {
    const id = playerState.group?.id;
    if (!id) return;
    untrack(() => syncGroup(playerState.group));
  });

  // 切卡注入：只依赖 index 与组 id，其余 untrack（同 PlayerPage 的范式，
  // 防轮询换 words 引用误触发）；syncCard 内部 800ms 防抖 + slug 去重。
  $effect(() => {
    void playerState.index;
    const id = playerState.group?.id;
    if (!id) return;
    untrack(() => {
      const words = playerState.group?.words ?? [];
      if (!words.length) return;
      const w = words[playerState.index % words.length];
      if (w) syncCard(w.word, playerState.cards[w.slug]);
    });
  });

  function onFabClick() {
    if (!active) {
      // startTeacher 必须留在点击手势栈内（iOS AudioContext/麦克风权限）
      startTeacher();
      open = true;
    } else {
      open = !open;
    }
  }

  function onStop() {
    stopTeacher();
    open = false;
  }

  function onPhoto(event) {
    const file = event.target.files?.[0];
    event.target.value = ''; // 同一张照片可再拍再发
    if (file) sendPhoto(file);
  }
</script>

{#if open && active}
  <!-- sheet-layer：PlayerPage 的全局翻卡手势按该类名放行，面板内拖动不翻卡 -->
  <div class="panel sheet-layer">
    <div class="panel-head">
      <span class="dot {teacherState.status}"></span>
      <span class="panel-status">{statusText}</span>
      <button class="panel-close" onclick={() => (open = false)} aria-label="收起">▾</button>
    </div>
    {#if teacherState.transcript.length > 0}
      <!-- column-reverse + 倒序渲染：视觉仍是时间正序，视口天然锚在最新一条 -->
      <div class="transcript">
        {#each [...teacherState.transcript].reverse() as t}
          <p class="line {t.role}">
            <span class="who">{t.role === 'teacher' ? '老师' : '我'}</span>{t.text}
          </p>
        {/each}
      </div>
    {:else}
      <p class="hint">
        跟老师打个招呼吧！可以说：<br />
        “Teacher, 介绍一下这个单词” · “我们来听写吧” · 或随便聊聊<br />
        也可以拍张照片，问老师 “Look at this!”
      </p>
    {/if}
    <div class="btn-row">
      <!-- label 套 input：点击直接调起相机（capture），无需 JS 转发手势 -->
      <label class="btn photo-btn" class:disabled={teacherState.status === 'connecting'}>
        📷 拍照
        <input
          type="file"
          accept="image/*"
          capture="environment"
          onchange={onPhoto}
          disabled={teacherState.status === 'connecting'}
          hidden
        />
      </label>
      <button class="btn stop-btn" onclick={onStop}>结束对话</button>
    </div>
  </div>
{/if}

<button
  class="teacher-fab sheet-layer {teacherState.status}"
  class:unsupported={!teacherState.supported}
  onclick={onFabClick}
  aria-label="AI 老师"
>
  <span class="fab-icon">🧑‍🏫</span>
  {#if teacherState.status === 'connecting'}
    <span class="fab-ring"></span>
  {/if}
  {#if teacherState.status === 'error'}
    <span class="fab-err">!</span>
  {/if}
</button>

<style>
  /* 首页右下角已有「＋ 导入」FAB（bottom≈24px），老师按钮上移错开 */
  .teacher-fab {
    position: fixed;
    right: max(20px, env(safe-area-inset-right));
    bottom: calc(max(24px, env(safe-area-inset-bottom)) + 76px);
    z-index: 900; /* Toast 是 1000，压过页面内容但不盖提示 */
    width: 56px;
    height: 56px;
    border-radius: 50%;
    border: none;
    background: #fff;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18);
    font-size: 28px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    -webkit-tap-highlight-color: transparent;
  }
  .teacher-fab:active {
    transform: scale(0.94);
  }
  .teacher-fab.listening {
    box-shadow:
      0 6px 20px rgba(0, 0, 0, 0.18),
      0 0 0 3px #34c759;
  }
  .teacher-fab.speaking {
    box-shadow:
      0 6px 20px rgba(0, 0, 0, 0.18),
      0 0 0 3px var(--primary);
    animation: breathe 1.6s ease-in-out infinite;
  }
  .teacher-fab.unsupported {
    filter: saturate(0.2);
    opacity: 0.75;
  }
  @keyframes breathe {
    50% {
      transform: scale(1.07);
    }
  }
  .fab-icon {
    pointer-events: none;
  }
  .fab-ring {
    position: absolute;
    inset: -3px;
    border-radius: 50%;
    border: 3px solid var(--primary);
    border-top-color: transparent;
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .fab-err {
    position: absolute;
    top: -2px;
    right: -2px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: #ff3b30;
    color: #fff;
    font-size: 14px;
    font-weight: 800;
    line-height: 20px;
  }

  .panel {
    position: fixed;
    right: max(16px, env(safe-area-inset-right));
    bottom: calc(max(24px, env(safe-area-inset-bottom)) + 140px);
    z-index: 900;
    width: min(320px, calc(100vw - 32px));
    max-height: 46vh;
    display: flex;
    flex-direction: column;
    background: #fff;
    border-radius: 16px;
    box-shadow: 0 12px 36px rgba(0, 0, 0, 0.22);
    padding: 12px;
    gap: 10px;
  }
  .panel-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #bbb;
    flex: none;
  }
  .dot.listening {
    background: #34c759;
  }
  .dot.speaking {
    background: var(--primary);
    animation: breathe 1.2s ease-in-out infinite;
  }
  .dot.connecting {
    background: #ffcc00;
  }
  .panel-status {
    font-size: 14px;
    font-weight: 700;
    flex: 1;
  }
  .panel-close {
    border: none;
    background: none;
    font-size: 18px;
    padding: 4px 8px;
    cursor: pointer;
    color: #888;
  }
  .transcript {
    overflow-y: auto;
    display: flex;
    flex-direction: column-reverse;
    gap: 6px;
    min-height: 60px;
  }
  .line {
    margin: 0;
    font-size: 14px;
    line-height: 1.5;
    word-break: break-word;
  }
  .line .who {
    display: inline-block;
    font-weight: 800;
    margin-right: 6px;
    color: #999;
    font-size: 12px;
  }
  .line.teacher .who {
    color: var(--primary);
  }
  .hint {
    margin: 0;
    font-size: 13px;
    color: #888;
    line-height: 1.7;
  }
  .btn-row {
    display: flex;
    gap: 8px;
  }
  .btn-row .btn {
    flex: 1;
    min-height: 44px;
    border: none;
    border-radius: 12px;
    background: #f3f3f5;
    font-size: 15px;
    font-weight: 700;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    -webkit-tap-highlight-color: transparent;
  }
  .photo-btn {
    color: #333;
  }
  .photo-btn.disabled {
    opacity: 0.5;
    cursor: default;
  }
  .stop-btn {
    color: #d33;
  }
</style>
