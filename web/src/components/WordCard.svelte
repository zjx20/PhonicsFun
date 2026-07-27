<script>
  import ChunkRow from './ChunkRow.svelte';
  import PlayButtons from './PlayButtons.svelte';
  import CardMenu from './CardMenu.svelte';
  import { wordAudioUrl } from '../lib/api.js';

  // card: card.json；word: 组详情里的状态项 {word,slug,text,audio,error?}；
  // regenerating: player 状态里的在途重新生成任务（null 表示没有）。
  let { card, word, regenerating = null, onregenerate } = $props();

  let menuOpen = $state(false);

  const chunks = $derived(card.chunks ?? []);
  // 数据校验：chunks[].grapheme 依序拼接（转小写）必须精确等于 word
  const spellingOk = $derived(
    chunks.map((c) => c.grapheme).join('').toLowerCase() === (card.word ?? '').toLowerCase()
  );
  const audioReady = $derived(word.audio === 'done' && !regenerating);
  const blendUrl = $derived(wordAudioUrl(word.slug, 'blend', card.generated_at));
  const wordUrl = $derived(wordAudioUrl(word.slug, 'word', card.generated_at));
</script>

<div class="word-card">
  {#if regenerating}
    <div class="regen-banner pulse">⏳ 重新生成中…</div>
  {/if}

  {#if !spellingOk}
    <div class="warn-banner">
      <span>⚠ 拆解与拼写不一致，建议重新生成</span>
      <button class="btn btn-small btn-warn" onclick={() => onregenerate(word.slug, 'text')}>
        重新生成文本
      </button>
    </div>
  {/if}

  <button class="icon-btn card-menu-btn" aria-label="更多操作" onclick={() => (menuOpen = true)}>⋮</button>

  <div class="word-big">
    {#if spellingOk}
      {#each chunks as chunk, i}
        <span class="word-chunk" class:silent-chunk={chunk.silent} style="color: var(--c{i % 6})">
          {chunk.grapheme}
        </span>
      {/each}
    {:else}
      <span class="word-plain">{card.word}</span>
    {/if}
  </div>

  {#if card.ipa}
    <div class="ipa">{card.ipa}</div>
  {/if}

  <ChunkRow {chunks} />

  <div class="defs">
    {#if card.definition_zh}
      <p class="def-zh">{card.definition_zh}</p>
    {/if}
    {#if card.definition_en}
      <p class="def-en">{card.definition_en}</p>
    {/if}
  </div>

  {#if word.audio === 'failed' && !regenerating}
    <div class="audio-failed">
      <span>⚠ 音频生成失败</span>
      <button class="btn btn-small btn-warn" onclick={() => onregenerate(word.slug, 'audio')}>
        重试音频
      </button>
    </div>
  {/if}

  <PlayButtons {blendUrl} {wordUrl} disabled={!audioReady} />
</div>

<CardMenu
  open={menuOpen}
  word={card.word}
  onclose={() => (menuOpen = false)}
  onconfirm={(target) => {
    menuOpen = false;
    onregenerate(word.slug, target);
  }}
/>

<style>
  .word-card {
    position: relative;
  }
  .regen-banner {
    margin: -6px 0 12px;
    padding: 10px 14px;
    border-radius: 12px;
    background: var(--primary-soft);
    color: var(--primary-dark);
    font-size: 15px;
    font-weight: 700;
    text-align: center;
  }
  .warn-banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin: -6px 0 12px;
    padding: 10px 14px;
    border-radius: 12px;
    background: var(--red-soft);
    color: var(--red);
    font-size: 14px;
    font-weight: 700;
  }
  .card-menu-btn {
    position: absolute;
    top: 6px;
    right: 6px;
    z-index: 5;
  }
  .word-big {
    margin-top: 8px;
    text-align: center;
    font-size: clamp(44px, 14vw, 68px);
    font-weight: 800;
    letter-spacing: 0.02em;
    line-height: 1.15;
    word-break: break-all;
  }
  .word-chunk.silent-chunk {
    color: var(--muted) !important;
    opacity: 0.75;
  }
  .word-plain {
    color: var(--text);
  }
  .ipa {
    margin-top: 4px;
    text-align: center;
    font-size: 17px;
    color: var(--muted);
  }
  .defs {
    margin-top: 16px;
    text-align: center;
  }
  .def-zh {
    font-size: 19px;
    font-weight: 700;
    color: var(--text);
  }
  .def-en {
    margin-top: 4px;
    font-size: 14px;
    color: var(--muted);
  }
  .audio-failed {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    margin-top: 14px;
    font-size: 14px;
    font-weight: 700;
    color: var(--red);
  }
</style>
