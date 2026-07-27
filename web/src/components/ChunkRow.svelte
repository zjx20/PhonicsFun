<script>
  // chunk 拆解条：每个 chunk 一列（grapheme 大字 / respell / IPA 音素），
  // 同列同一浅色底；silent 的 chunk 灰色斜体并标注“不发音”。
  let { chunks } = $props();
</script>

<div class="chunk-row">
  {#each chunks as chunk, i}
    <div
      class="chunk-col"
      class:silent={chunk.silent}
      style={chunk.silent ? '' : `background: var(--c${i % 6}-bg); color: var(--c${i % 6})`}
    >
      <div class="chunk-grapheme" class:silent-text={chunk.silent}>{chunk.grapheme}</div>
      {#if chunk.silent}
        <div class="chunk-silent-note">不发音</div>
      {:else}
        <div class="chunk-respell">{chunk.respell}</div>
        <div class="chunk-phoneme">{chunk.phoneme}</div>
      {/if}
    </div>
  {/each}
</div>

<style>
  .chunk-row {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 8px;
    margin-top: 18px;
  }
  .chunk-col {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-start;
    gap: 2px;
    min-width: 64px;
    padding: 10px 12px;
    border-radius: 14px;
  }
  .chunk-col.silent {
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
  .chunk-respell {
    font-size: 15px;
    font-weight: 700;
  }
  .chunk-phoneme {
    font-size: 13px;
    opacity: 0.8;
  }
  .chunk-silent-note {
    font-size: 12px;
    color: var(--muted);
  }
</style>
