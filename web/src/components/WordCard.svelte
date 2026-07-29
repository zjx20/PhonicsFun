<script>
  import ChunkRow from './ChunkRow.svelte';
  import PlayButtons from './PlayButtons.svelte';
  import CardMenu from './CardMenu.svelte';
  import { wordAudioUrl } from '../lib/api.js';
  import { playback, playSegment } from '../lib/playback.svelte.js';

  // card: card.json v2（syllables/senses/examples）；word: 组详情里的状态项
  // {word,slug,text,audio,audioVersion?,error?}；regenerating: 在途重新生成任务
  //（null 表示没有）；cues: blend.wav 的时间标注（null = 后端无 cues，
  // 点读/高亮降级，其余照常）。
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
  const blendUrl = $derived(wordAudioUrl(word.slug, 'blend', word.audioVersion));
  const wordUrl = $derived(wordAudioUrl(word.slug, 'word', word.audioVersion));

  // —— 高亮（纯派生，无本地播放状态）——
  // 整段播放 blend.wav 时按播放进度命中 cue（playback.time 由播放层 rAF 驱动）；
  // 点读时直接高亮被点的那段。cue 全部传给 ChunkRow 处理：chunk 亮格子、
  // syllable 亮音节胶囊、tail/word 框住整个音节区（大字区不参与动效）。
  // word 子区间与 tail 重叠且排在其后：整段播放的 time-range 命中总是先取到
  // tail，word 只在点大字区的精确 seg 匹配时命中。播放结束后 playback 复位，
  // 这里自然归 null。
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

  // 大字区自适应字号：完整单词尽量一行放下，长单词缩小字体而不是换行。
  // 按"可用宽度 ÷ (字符数 × 经验字符宽)"估算，0.64em/字符是 800 字重 +
  // 0.02em letter-spacing 下的保守近似；极端情况仍放不下时由 break-all 兜底。
  let bigWrapW = $state(0);
  const bigFontPx = $derived.by(() => {
    const len = (card.word ?? '').length;
    if (!bigWrapW || !len) return null; // 首帧未测宽，先用 CSS 兜底字号
    return Math.max(24, Math.min(68, Math.floor(bigWrapW / (len * 0.64))));
  });

  // 点大字区 = 播一遍完整读音（无串读）：blend.wav tail 段内的 word 子区间，
  // 与"整词"按钮同源。旧 cues 没有 word 段时大字区不可点，其余照常。
  const wordCue = $derived(cues?.cues?.find((c) => c.kind === 'word') ?? null);
  const canTapWord = $derived(audioReady && !!wordCue);
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

  <!-- 顶部操作行：播放按钮与 ⋮ 同排，利用卡片顶部空间，下方不再放大按钮行 -->
  <div class="card-top">
    <PlayButtons {blendUrl} {wordUrl} disabled={!audioReady} />
    <button class="icon-btn" aria-label="更多操作" onclick={() => (menuOpen = true)}>⋮</button>
  </div>

  <div class="word-big-wrap" bind:clientWidth={bigWrapW}>
    <button
      type="button"
      class="word-big"
      class:tappable={canTapWord}
      style={bigFontPx ? `font-size:${bigFontPx}px` : ''}
      disabled={!canTapWord}
      onclick={() => playSegment(blendUrl, wordCue.start_ms, wordCue.end_ms)}
    >
      {#if spellingOk}
        {#each syllables as syl, s}{#each syl.chunks ?? [] as chunk, c}<span
              class="word-chunk"
              class:silent-chunk={chunk.silent}
              style="color: var(--c{(offsets[s] + c) % 6})">{chunk.grapheme}</span>{/each}{/each}
      {:else}
        <span class="word-plain">{card.word}</span>
      {/if}
    </button>
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
</div>

<CardMenu
  open={menuOpen}
  word={card.word}
  onclose={() => (menuOpen = false)}
  onconfirm={(target, feedback) => {
    menuOpen = false;
    onregenerate(word.slug, target, feedback);
  }}
/>

<style>
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
  .card-top {
    display: flex;
    align-items: center;
    gap: 8px;
    /* 负 margin 让操作行贴近卡片边角，省出垂直空间给下方内容 */
    margin: -8px -8px 0;
  }
  .word-big-wrap {
    margin-top: 8px;
  }
  .word-big {
    display: block;
    width: 100%;
    padding: 0;
    border: none;
    background: none;
    font-family: inherit;
    text-align: center;
    /* 兜底字号：脚本测宽后由 inline style 按单词长度覆盖，保证不换行 */
    font-size: clamp(44px, 14vw, 68px);
    font-weight: 800;
    letter-spacing: 0.02em;
    line-height: 1.15;
    word-break: break-all;
    cursor: default;
    transition: transform 0.12s ease;
  }
  .word-big:disabled {
    /* 无 word cue（旧数据降级）时仅不可点，外观保持正常 */
    opacity: 1;
  }
  .word-big.tappable {
    cursor: pointer;
  }
  .word-big.tappable:active {
    transform: scale(0.96);
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
