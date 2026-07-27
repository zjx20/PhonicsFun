<script>
  import ChunkRow from './ChunkRow.svelte';
  import PlayButtons from './PlayButtons.svelte';
  import CardMenu from './CardMenu.svelte';
  import { wordAudioUrl } from '../lib/api.js';
  import { playback } from '../lib/playback.svelte.js';

  // card: card.json v2（syllables/senses/examples）；word: 组详情里的状态项
  // {word,slug,text,audio,error?}；regenerating: 在途重新生成任务（null 表示没有）；
  // cues: blend.wav 的时间标注（null = 后端无 cues，点读/高亮降级，其余照常）。
  let { card, word, cues = null, regenerating = null, onregenerate } = $props();

  let menuOpen = $state(false);

  const syllables = $derived(card.syllables ?? []);
  // 每个音节的跨音节起始 chunk 序号：大字区与 ChunkRow 用同一套连续编号取色
  const offsets = $derived.by(() => {
    const out = [];
    let n = 0;
    for (const syl of syllables) {
      out.push(n);
      n += syl.chunks?.length ?? 0;
    }
    return out;
  });
  // v2 校验：syllables[].text 依序拼接 === word 小写，
  // 且每个音节内 chunks[].grapheme 拼接 === 音节 text
  const spellingOk = $derived.by(() => {
    if (syllables.length === 0) return false;
    if (syllables.map((s) => s.text).join('') !== (card.word ?? '').toLowerCase()) return false;
    return syllables.every((s) => (s.chunks ?? []).map((c) => c.grapheme).join('') === s.text);
  });
  const audioReady = $derived(word.audio === 'done' && !regenerating);
  const blendUrl = $derived(wordAudioUrl(word.slug, 'blend', card.generated_at));
  const wordUrl = $derived(wordAudioUrl(word.slug, 'word', card.generated_at));

  // —— 高亮（纯派生，无本地播放状态）——
  // 整段播放 blend.wav 时按播放进度命中 cue（playback.time 由播放层 rAF 驱动）；
  // 点读时直接高亮被点的那段。chunk/syllable cue 传给 ChunkRow，tail cue
  // 高亮顶部大字区。播放结束后 playback 复位，这里自然归 null。
  const activeCue = $derived.by(() => {
    const list = cues?.cues;
    if (!list?.length) return null;
    const seg = playback.segment;
    if (seg && seg.url === blendUrl) {
      return list.find((c) => c.start_ms === seg.startMs && c.end_ms === seg.endMs) ?? null;
    }
    if (playback.url === blendUrl) {
      return list.find((c) => playback.time >= c.start_ms && playback.time < c.end_ms) ?? null;
    }
    return null;
  });
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

  <div class="word-big" class:tail-active={activeCue?.kind === 'tail'}>
    {#if spellingOk}
      {#each syllables as syl, s}
        {#if s > 0}<span class="syl-sep">·</span>{/if}
        {#each syl.chunks ?? [] as chunk, c}
          <span
            class="word-chunk"
            class:silent-chunk={chunk.silent}
            style="color: var(--c{(offsets[s] + c) % 6})"
          >
            {chunk.grapheme}
          </span>
        {/each}
      {/each}
    {:else}
      <span class="word-plain">{card.word}</span>
    {/if}
  </div>

  {#if card.ipa}
    <div class="ipa">{card.ipa}</div>
  {/if}

  <ChunkRow {syllables} {blendUrl} {cues} canPlay={audioReady && !!cues} {activeCue} />

  <div class="senses">
    {#each card.senses ?? [] as sense}
      <div class="sense">
        <p class="sense-main">
          {#if sense.pos}<span class="pos-badge">{sense.pos}</span>{/if}
          <span class="sense-zh">{sense.zh}</span>
        </p>
        {#if sense.en}
          <p class="sense-en">{sense.en}</p>
        {/if}
      </div>
    {/each}
  </div>

  {#if (card.examples ?? []).length > 0}
    <div class="examples">
      {#each card.examples as ex}
        <div class="example">
          <p class="example-en">{ex.en}</p>
          {#if ex.zh}
            <p class="example-zh">{ex.zh}</p>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

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
    /* 宽度随单词收缩：tail 高亮的胶囊框紧贴内容，不横跨整卡蹭到右上角 ⋮ 按钮；
       max-width 再给两侧留出按钮的空位 */
    width: fit-content;
    max-width: calc(100% - 88px);
    margin: 8px auto 0;
    padding: 2px 16px;
    text-align: center;
    font-size: clamp(44px, 14vw, 68px);
    font-weight: 800;
    letter-spacing: 0.02em;
    line-height: 1.15;
    word-break: break-all;
    border-radius: 18px;
    transition:
      background 0.15s ease,
      box-shadow 0.15s ease;
  }
  .word-big.tail-active {
    background: var(--primary-soft);
    box-shadow: 0 0 0 3px var(--primary);
  }
  .syl-sep {
    color: var(--dot);
    font-weight: 400;
    margin: 0 0.04em;
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
  .senses {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 16px;
    text-align: center;
  }
  .sense-main {
    display: flex;
    align-items: baseline;
    justify-content: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .pos-badge {
    flex-shrink: 0;
    align-self: center;
    padding: 1px 8px;
    border-radius: 999px;
    background: var(--primary-soft);
    color: var(--primary-dark);
    font-size: 12px;
    font-weight: 700;
    white-space: nowrap;
  }
  .sense-zh {
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
  }
  .sense-en {
    margin-top: 2px;
    font-size: 13px;
    color: var(--muted);
  }
  .examples {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1.5px solid var(--track);
    text-align: left;
  }
  .example-en {
    font-size: 15px;
    color: var(--text);
  }
  .example-zh {
    margin-top: 1px;
    font-size: 13px;
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
