<script>
  // 未就绪 / 失败单词的占位卡，与真实卡片一起参与循环。
  let { word, onretry } = $props();

  const failed = $derived(word.text === 'failed' || word.audio === 'failed');
</script>

<div class="word-card placeholder">
  <div class="ph-word">{word.word}</div>

  {#if failed}
    <div class="ph-error">
      <p class="ph-error-title">⚠ 生成失败</p>
      {#if word.error}
        <p class="ph-error-detail">{word.error}</p>
      {/if}
      <button class="btn btn-primary" onclick={() => onretry(word.slug)}>🔄 重试</button>
    </div>
  {:else}
    <div class="ph-pending pulse">
      <div class="skeleton-line w70"></div>
      <div class="skeleton-line w90"></div>
      <div class="skeleton-line w50"></div>
      <p class="ph-pending-text">生成中…</p>
    </div>
  {/if}
</div>

<style>
  .placeholder {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 24px;
    min-height: 380px;
    justify-content: center;
  }
  .ph-word {
    font-size: clamp(40px, 13vw, 64px);
    font-weight: 800;
    color: var(--muted);
    letter-spacing: 0.02em;
    word-break: break-all;
    text-align: center;
  }
  .ph-pending {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
    width: 100%;
  }
  .skeleton-line {
    height: 16px;
    border-radius: 8px;
    background: var(--track);
  }
  .w70 {
    width: 70%;
  }
  .w90 {
    width: 90%;
  }
  .w50 {
    width: 50%;
  }
  .ph-pending-text {
    margin-top: 8px;
    font-size: 17px;
    font-weight: 700;
    color: var(--muted);
  }
  .ph-error {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    text-align: center;
  }
  .ph-error-title {
    font-size: 18px;
    font-weight: 800;
    color: var(--red);
  }
  .ph-error-detail {
    font-size: 14px;
    color: var(--muted);
    word-break: break-word;
    max-width: 100%;
  }
</style>
