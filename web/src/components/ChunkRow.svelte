<script>
  // 拆解区：每个音节一个分组容器，组内是 chunk 列（grapheme 大字 + IPA 音素），
  // 组下方居中显示音节 text。silent 的 chunk 灰色斜体并标注“不发音”。
  // chunk 着色用跨音节的连续序号 i 取 --c{i%6}，与 WordCard 大字着色保持同一编号。
  //
  // 点读：有 cues 且 canPlay 时，点 chunk 格 / 音节标签播放对应音频片段（Web Audio，
  // 必须在点击处理器里同步调用 playSegment，iOS Safari 要求手势内创建 AudioContext）。
  // 无 cues（旧数据 404 降级）时一律不可点，也不带可点样式。
  //
  // 卡拉OK：activeCue 由 WordCard 在整段播放 blend.wav 时轮询命中传入——
  // chunk cue 高亮对应格子，syllable cue 高亮整个音节组（tail 由 WordCard 自己处理）。
  import { playSegment } from '../lib/audio.js';
  import { toastError } from '../lib/toast.svelte.js';

  let { syllables, blendUrl = '', cues = null, canPlay = false, activeCue = null } = $props();

  // 每个音节的跨音节起始 chunk 序号（含 silent chunk，与大字区编号一致）
  const groups = $derived.by(() => {
    let offset = 0;
    return (syllables ?? []).map((syl, s) => {
      const start = offset;
      offset += syl.chunks?.length ?? 0;
      return { syl, s, start };
    });
  });

  // cue 查找表：chunk cue 按 "音节:chunk" 下标索引（下标含 silent chunk，
  // 但 silent chunk 不会有 cue），syllable cue 按音节下标索引。
  const chunkCues = $derived.by(() => {
    const m = new Map();
    for (const cue of cues?.cues ?? []) {
      if (cue.kind === 'chunk') m.set(`${cue.syllable}:${cue.chunk}`, cue);
    }
    return m;
  });
  const sylCues = $derived.by(() => {
    const m = new Map();
    for (const cue of cues?.cues ?? []) {
      if (cue.kind === 'syllable') m.set(cue.syllable, cue);
    }
    return m;
  });

  function play(cue) {
    if (!canPlay || !cue) return;
    playSegment(blendUrl, cue.start_ms, cue.end_ms, {
      onError: () => toastError('音频播放失败'),
    });
  }

  const isChunkActive = (s, c) =>
    activeCue?.kind === 'chunk' && activeCue.syllable === s && activeCue.chunk === c;
  const isSylActive = (s) => activeCue?.kind === 'syllable' && activeCue.syllable === s;
</script>

<div class="syllable-row">
  {#each groups as { syl, s, start } (s)}
    <div class="syllable-group" class:active={isSylActive(s)}>
      <div class="chunk-list">
        {#each syl.chunks ?? [] as chunk, c}
          {@const i = start + c}
          {@const cue = chunkCues.get(`${s}:${c}`)}
          <button
            type="button"
            class="chunk-cell"
            class:silent={chunk.silent}
            class:tappable={canPlay && !!cue}
            class:active={isChunkActive(s, c)}
            style={chunk.silent ? '' : `background: var(--c${i % 6}-bg); color: var(--c${i % 6})`}
            disabled={!canPlay || !cue}
            onclick={() => play(cue)}
          >
            <span class="chunk-grapheme" class:silent-text={chunk.silent}>{chunk.grapheme}</span>
            {#if chunk.silent}
              <span class="chunk-silent-note">不发音</span>
            {:else}
              <span class="chunk-phoneme">{chunk.phoneme}</span>
            {/if}
          </button>
        {/each}
      </div>
      <!-- 单音节词没有 syllable cue，标签与整词重复，直接隐藏 -->
      {#if groups.length > 1}
        {@const sylCue = sylCues.get(s)}
        <button
          type="button"
          class="syllable-label"
          class:tappable={canPlay && !!sylCue}
          disabled={!canPlay || !sylCue}
          onclick={() => play(sylCue)}
        >
          {syl.text}
        </button>
      {/if}
    </div>
  {/each}
</div>

<style>
  .syllable-row {
    display: flex;
    flex-wrap: wrap; /* 小屏允许音节组换行 */
    justify-content: center;
    align-items: flex-start;
    gap: 10px;
    margin-top: 18px;
  }
  .syllable-group {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 8px 8px 4px;
    border-radius: 18px;
    background: var(--bg);
    border: 1.5px solid var(--track);
    transition:
      box-shadow 0.12s ease,
      background 0.12s ease;
  }
  .syllable-group.active {
    background: var(--primary-soft);
    border-color: var(--primary);
    box-shadow: 0 0 0 2px var(--primary);
  }
  .chunk-list {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 6px;
  }
  .chunk-cell {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-start;
    gap: 2px;
    min-width: 56px;
    min-height: 64px;
    padding: 10px 12px;
    border: none;
    border-radius: 14px;
    font-family: inherit;
    cursor: default;
    transition:
      transform 0.12s ease,
      box-shadow 0.12s ease;
  }
  .chunk-cell:disabled {
    /* 无 cues 时仅去掉可点样式，外观保持正常，不做“禁用”淡化 */
    opacity: 1;
  }
  .chunk-cell.tappable {
    cursor: pointer;
  }
  .chunk-cell.tappable:active {
    transform: scale(0.94);
  }
  .chunk-cell.active {
    transform: scale(1.1);
    box-shadow: 0 0 0 3px currentColor;
  }
  .chunk-cell.silent {
    background: var(--track);
    color: var(--muted);
  }
  .chunk-grapheme {
    font-size: 28px;
    font-weight: 800;
    line-height: 1.2;
  }
  .chunk-grapheme.silent-text {
    font-style: italic;
    color: var(--muted);
  }
  .chunk-phoneme {
    font-size: 14px;
    opacity: 0.85;
  }
  .chunk-silent-note {
    font-size: 12px;
    color: var(--muted);
  }
  .syllable-label {
    min-width: 44px;
    min-height: 44px;
    padding: 0 12px;
    border: none;
    background: none;
    border-radius: 12px;
    font-family: inherit;
    font-size: 16px;
    font-weight: 700;
    color: var(--muted);
    cursor: default;
  }
  .syllable-label.tappable {
    cursor: pointer;
    color: var(--primary-dark);
  }
  .syllable-label.tappable:active {
    background: var(--primary-soft);
  }
</style>
