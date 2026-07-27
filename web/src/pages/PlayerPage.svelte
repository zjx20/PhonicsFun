<script>
  import { fly } from 'svelte/transition';
  import {
    playerState,
    openGroup,
    closeGroup,
    nextCard,
    prevCard,
    regenerate,
  } from '../lib/player.svelte.js';
  import { stopPlayback, preload } from '../lib/playback.svelte.js';
  import { wordAudioUrl } from '../lib/api.js';
  import WordCard from '../components/WordCard.svelte';
  import CardPlaceholder from '../components/CardPlaceholder.svelte';

  let { id } = $props();

  $effect(() => {
    openGroup(id);
    return () => {
      closeGroup();
      stopPlayback();
    };
  });

  const words = $derived(playerState.group?.words ?? []);
  const current = $derived(words.length > 0 ? words[playerState.index % words.length] : null);
  const currentCard = $derived(current ? (playerState.cards[current.slug] ?? null) : null);
  const readyCount = $derived(words.filter((w) => w.text === 'done' && w.audio === 'done').length);

  // 预加载前后相邻卡片的两段音频
  $effect(() => {
    const len = words.length;
    if (len === 0) return;
    for (const offset of [1, len - 1]) {
      const w = words[(playerState.index + offset) % len];
      const card = playerState.cards[w.slug];
      if (card && w.audio === 'done') {
        preload(wordAudioUrl(w.slug, 'word', card.generated_at));
        preload(wordAudioUrl(w.slug, 'blend', card.generated_at));
      }
    }
  });

  // 换卡时停止当前播放
  $effect(() => {
    void playerState.index;
    stopPlayback();
  });

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
        nextCard();
      } else if (dragX >= SWIPE_THRESHOLD) {
        swallowNextClick();
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
  <a class="back-btn" href="#/">‹ 返回</a>
  <div class="header-title">
    <h1>{playerState.group?.name || '单词组'}</h1>
    {#if words.length > 0}
      <span class="header-sub" class:all-ready={readyCount === words.length}>
        {readyCount === words.length ? '全部就绪 ✓' : `就绪 ${readyCount}/${words.length}`}
      </span>
    {/if}
  </div>
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
    <div
      class="card-stage"
      style:transform="translateX({dragX}px)"
      style:transition={dragging ? 'none' : 'transform 0.2s ease'}
      onpointerdown={onPointerDown}
    >
      {#key playerState.index}
        <div class="card-slot" in:fly={{ x: playerState.dir * 90, duration: 200 }}>
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
  .card-stage {
    touch-action: pan-y;
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
