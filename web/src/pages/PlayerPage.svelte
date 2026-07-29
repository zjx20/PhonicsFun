<script>
  import { untrack } from 'svelte';
  import { cubicOut } from 'svelte/easing';
  import {
    playerState,
    prefs,
    toggleAutoPlay,
    openGroup,
    closeGroup,
    nextCard,
    prevCard,
    regenerate,
  } from '../lib/player.svelte.js';
  import { playback, stopPlayback, playFull, preload } from '../lib/playback.svelte.js';
  import { wordAudioUrl } from '../lib/api.js';
  import WordCard from '../components/WordCard.svelte';
  import CardPlaceholder from '../components/CardPlaceholder.svelte';

  let { id, word = '' } = $props();

  $effect(() => {
    openGroup(id, word);
    return () => {
      closeGroup();
      stopPlayback();
    };
  });

  const words = $derived(playerState.group?.words ?? []);
  const current = $derived(words.length > 0 ? words[playerState.index % words.length] : null);
  const currentCard = $derived(current ? (playerState.cards[current.slug] ?? null) : null);
  const readyCount = $derived(words.filter((w) => w.text === 'done' && w.audio === 'done').length);

  // 预加载前后相邻卡片的两段音频（URL 版本号 = audioVersion，与 WordCard 一致）
  $effect(() => {
    const len = words.length;
    if (len === 0) return;
    for (const offset of [1, len - 1]) {
      const w = words[(playerState.index + offset) % len];
      if (w.audio === 'done') {
        preload(wordAudioUrl(w.slug, 'word', w.audioVersion));
        preload(wordAudioUrl(w.slug, 'blend', w.audioVersion));
      }
    }
  });

  // 自动播放在翻页后等这么久再开口：让 280ms 翻页动画先落定，卡片
  // 站稳了再读，节奏不赶。
  const AUTO_PLAY_DELAY_MS = 500;

  // 换卡时停止当前播放；开着「自动播放」则延迟片刻后播新卡的拼读。
  // 只依赖 index：开关切换、轮询刷新（words 数组换引用）都不触发——
  // 用 untrack 读其余状态，否则轮询会每 2 秒重播一次。首次进组 index
  // 未变化，不自动播（"翻页之后"才播，也天然避开无手势的 autoplay 限制）。
  // 延迟期间再翻页/离开页面由 effect cleanup 取消定时器；用户抢先手动
  // 点了播放（整段或点读）则让位，不打断。
  $effect(() => {
    void playerState.index;
    stopPlayback();
    const url = untrack(() => {
      if (!prefs.autoPlay) return null;
      const w = current;
      if (!w || w.audio !== 'done' || playerState.regenerating[w.slug]) return null;
      return wordAudioUrl(w.slug, 'blend', w.audioVersion);
    });
    if (!url) return;
    const timer = setTimeout(() => {
      if (playback.url || playback.segment) return;
      playFull(url);
    }, AUTO_PLAY_DELAY_MS);
    return () => clearTimeout(timer);
  });

  // —— 翻页动效 ——
  // 旧卡从当前位置继续滑出到一侧、新卡同时从另一侧滑入（{#key} 重建 +
  // 自定义 in/out，新旧 slot 靠 grid-area 1/1 重叠）。手势翻页时 slideOut
  // 以松手瞬间的拖动位移为起点，出场与拖动无缝衔接，不会先弹回再换内容。
  let stageW = $state(600); // bind:clientWidth，滑动距离 = 舞台宽度
  let releaseX = 0; // 松手触发翻页瞬间的拖动位移；按钮/键盘翻页时为 0

  function slideOut(node) {
    const from = releaseX;
    releaseX = 0;
    const to = -playerState.dir * stageW;
    return {
      duration: 280,
      easing: cubicOut,
      css: (t, u) => `transform: translateX(${from + (to - from) * u}px)`,
    };
  }

  function slideIn(node) {
    const from = playerState.dir * stageW;
    return {
      duration: 280,
      easing: cubicOut,
      css: (t, u) => `transform: translateX(${u * from}px)`,
    };
  }

  // —— 左右滑动（pointer 事件，50px 阈值）——
  const SWIPE_THRESHOLD = 50;
  let dragX = $state(0);
  let dragging = $state(false); // 已判定为水平拖动
  let tracking = false;
  let startX = 0;
  let startY = 0;

  function onPointerDown(e) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    // 底部弹层内的手势不参与翻卡
    if (e.target.closest?.('.sheet-layer')) return;
    tracking = true;
    dragging = false;
    startX = e.clientX;
    startY = e.clientY;
    dragX = 0;
  }

  function onPointerMove(e) {
    if (!tracking) return;
    const dx = e.clientX - startX;
    const dy = e.clientY - startY;
    if (!dragging) {
      if (Math.abs(dx) > 10 && Math.abs(dx) > Math.abs(dy)) {
        dragging = true;
      } else if (Math.abs(dy) > 14 && Math.abs(dy) > Math.abs(dx)) {
        tracking = false; // 认定为竖直滚动，放弃本次手势
        return;
      } else {
        return;
      }
    }
    dragX = dx;
  }

  function onPointerUp() {
    if (!tracking) return;
    tracking = false;
    if (dragging) {
      if (dragX <= -SWIPE_THRESHOLD) {
        swallowNextClick();
        releaseX = dragX;
        nextCard();
      } else if (dragX >= SWIPE_THRESHOLD) {
        swallowNextClick();
        releaseX = dragX;
        prevCard();
      }
    }
    dragging = false;
    dragX = 0;
  }

  // 滑动结束时手指若落在按钮上，浏览器仍会派发 click；吞掉这一次，避免误触
  function swallowNextClick() {
    const swallow = (e) => {
      e.stopPropagation();
      e.preventDefault();
    };
    window.addEventListener('click', swallow, true);
    setTimeout(() => window.removeEventListener('click', swallow, true), 250);
  }

  // 键盘左右方向键翻卡（桌面场景，与两侧按钮配套）
  function onKeyDown(e) {
    if (words.length === 0) return;
    if (e.target.closest?.('input, textarea, select')) return;
    if (e.key === 'ArrowLeft') {
      e.preventDefault();
      prevCard();
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      nextCard();
    }
  }
</script>

<svelte:window
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
  onkeydown={onKeyDown}
/>

<header class="page-header with-back">
  <a class="back-btn" href="#/">‹<span class="back-text"> 返回</span></a>
  <div class="header-title">
    <h1>{playerState.group?.name || '单词组'}</h1>
    {#if words.length > 0}
      <span class="header-sub" class:all-ready={readyCount === words.length}>
        {readyCount === words.length ? '全部就绪 ✓' : `就绪 ${readyCount}/${words.length}`}
      </span>
    {/if}
  </div>
  <button
    class="auto-toggle"
    class:on={prefs.autoPlay}
    aria-pressed={prefs.autoPlay}
    title="翻页后自动播放拼读"
    onclick={toggleAutoPlay}
  >
    自动播放
  </button>
  <a class="edit-link" href={`#/group/${encodeURIComponent(id)}/edit`} aria-label="单词列表与编辑">
    ≡
  </a>
</header>

<main class="page player-page">
  {#if playerState.loading && !playerState.group}
    <div class="center-hint">
      <span class="spinner"></span>
      <p>加载中…</p>
    </div>
  {:else if playerState.error && !playerState.group}
    <div class="center-hint">
      <p class="error-text">{playerState.error}</p>
      <button class="btn btn-primary" onclick={() => openGroup(id)}>重试</button>
    </div>
  {:else if words.length === 0}
    <div class="center-hint">
      <p>这个组还没有单词</p>
      <a class="btn btn-primary" href="#/">回首页</a>
    </div>
  {:else}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="card-stage" bind:clientWidth={stageW} onpointerdown={onPointerDown}>
      {#key playerState.index}
        <div
          class="card-slot"
          style:transform={dragging ? `translateX(${dragX}px)` : ''}
          style:transition={dragging ? 'none' : 'transform 0.2s ease'}
          in:slideIn
          out:slideOut
        >
          {#if currentCard}
            <WordCard
              card={currentCard}
              word={current}
              cues={playerState.cues[current.slug] ?? null}
              regenerating={playerState.regenerating[current.slug] ?? null}
              onregenerate={regenerate}
            />
          {:else}
            <CardPlaceholder word={current} onretry={(slug) => regenerate(slug, 'both')} />
          {/if}
        </div>
      {/key}
    </div>

    <!-- 宽屏：两侧固定位置按钮（垂直居中于视口，不随卡片高度浮动，鼠标不用挪）；
         窄屏：隐藏，保留下方按钮行 + 滑动手势 -->
    <button class="side-nav side-prev" aria-label="上一个" onclick={prevCard}>‹</button>
    <button class="side-nav side-next" aria-label="下一个" onclick={nextCard}>›</button>

    <div class="nav-row">
      <button class="btn nav-btn" onclick={prevCard}>‹ 上一个</button>
      <button class="btn nav-btn" onclick={nextCard}>下一个 ›</button>
    </div>

    <div class="dots">
      {#each words as w, i (w.slug)}
        <span
          class="dot"
          class:active={i === playerState.index}
          class:pending={!(w.text === 'done' && w.audio === 'done')}
        ></span>
      {/each}
    </div>
  {/if}
</main>

<style>
  .player-page {
    display: flex;
    flex-direction: column;
  }
  .header-title {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }
  .header-title h1 {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .header-sub {
    flex-shrink: 0;
    font-size: 13px;
    font-weight: 700;
    color: var(--muted);
  }
  .header-sub.all-ready {
    color: var(--green);
  }
  /* 「≡」单词列表：圆形图标钮，紧凑；语义与首页组卡的「≡ 列表」一致 */
  .edit-link {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    border: 2px solid var(--primary-soft-border);
    border-radius: 50%;
    font-size: 22px;
    font-weight: 700;
    color: var(--primary-dark);
    text-decoration: none;
  }
  .edit-link:active {
    background: var(--primary-soft);
  }
  /* 「自动播放」开关：文字 pill（模式开关用图标表意不清，始终保留文字），
     激活态橙底白字。margin-left:auto 把右侧按钮组推到最右 */
  .auto-toggle {
    margin-left: auto;
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    min-height: 40px;
    padding: 0 14px;
    border: 2px solid var(--primary-soft-border);
    border-radius: 999px;
    background: none;
    color: var(--muted);
    font-family: inherit;
    font-size: 14px;
    font-weight: 700;
    cursor: pointer;
  }
  .auto-toggle.on {
    border-color: var(--primary);
    background: var(--primary);
    color: #fff;
  }
  .auto-toggle:active {
    transform: scale(0.96);
  }
  /* 小屏：隐藏就绪计数和「返回」文字（‹ 箭头保留），给标题和开关让位 */
  @media (max-width: 480px) {
    .header-sub,
    .back-text {
      display: none;
    }
  }
  .card-stage {
    display: grid;
    touch-action: pan-y;
    /* 翻页时新旧卡横向滑过舞台边界，裁掉出界部分，避免视口横向溢出；
       只裁 x 轴（clip 可与 y 轴 visible 共存），卡片上下阴影不受影响 */
    overflow-x: clip;
  }
  .card-slot {
    /* 翻页动画期间新旧两个 slot 重叠在同一格，高度取较高者，不上下堆叠 */
    grid-area: 1 / 1;
    will-change: transform;
  }
  .nav-row {
    display: flex;
    gap: 12px;
    margin-top: 16px;
  }
  .nav-btn {
    flex: 1;
    min-height: 56px;
    font-size: 18px;
    background: var(--card);
    color: var(--primary-dark);
    border: 2px solid var(--primary-soft-border);
  }
  .nav-btn:active {
    background: var(--primary-soft);
  }
  .side-nav {
    display: none;
  }
  /* 页面列 560px + 两侧按钮各 56px + 间距，740px 起两侧放得下 */
  @media (min-width: 740px) {
    .nav-row {
      display: none;
    }
    .side-nav {
      display: flex;
      align-items: center;
      justify-content: center;
      position: fixed; /* 相对视口垂直居中：卡片高矮变化不影响按钮位置 */
      top: 50%;
      transform: translateY(-50%);
      width: 56px;
      height: 56px;
      border: 2px solid var(--primary-soft-border);
      border-radius: 50%;
      background: var(--card);
      color: var(--primary-dark);
      font-family: inherit;
      font-size: 30px;
      line-height: 1;
      padding-bottom: 4px; /* ‹ › 字形偏高，微调视觉居中 */
      cursor: pointer;
      z-index: 10;
      transition:
        background 0.12s ease,
        transform 0.12s ease;
    }
    .side-prev {
      left: calc(50% - 280px - 76px);
    }
    .side-next {
      right: calc(50% - 280px - 76px);
    }
    .side-nav:hover {
      background: var(--primary-soft);
    }
    .side-nav:active {
      transform: translateY(-50%) scale(0.92);
    }
  }
  .dots {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 7px;
    margin: 18px 0 8px;
  }
  .dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--dot);
  }
  .dot.pending {
    background: var(--track);
  }
  .dot.active {
    background: var(--primary);
    transform: scale(1.35);
  }
</style>
