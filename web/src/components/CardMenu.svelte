<script>
  // 卡片右上 ⋮ 的 bottom-sheet：先选目标，再二次确认，防手滑触发 AI 调用。
  let { open = false, word = '', onclose, onconfirm } = $props();

  let chosen = $state(null); // null | 'text' | 'audio' | 'both'

  $effect(() => {
    if (!open) chosen = null;
  });

  const labels = {
    text: '重新生成文本',
    audio: '重新生成音频',
    both: '全部重新生成',
  };
</script>

{#if open}
  <div class="sheet-layer">
    <button class="sheet-backdrop" aria-label="关闭" onclick={onclose}></button>
    <div class="bottom-sheet">
      {#if chosen}
        <p class="sheet-title">确认{labels[chosen]}？</p>
        <p class="sheet-sub">会重新调用 AI，覆盖「{word}」当前内容</p>
        <div class="sheet-actions">
          <button class="btn btn-danger sheet-action" onclick={() => onconfirm(chosen)}>确认</button>
          <button class="btn btn-ghost sheet-action" onclick={() => (chosen = null)}>取消</button>
        </div>
      {:else}
        <p class="sheet-title">「{word}」</p>
        <button class="sheet-item" onclick={() => (chosen = 'text')}>📝 重新生成文本</button>
        <button class="sheet-item" onclick={() => (chosen = 'audio')}>🔊 重新生成音频</button>
        <button class="sheet-item" onclick={() => (chosen = 'both')}>♻️ 全部重新生成</button>
        <button class="sheet-item sheet-cancel" onclick={onclose}>取消</button>
      {/if}
    </div>
  </div>
{/if}

<style>
  .sheet-layer {
    position: fixed;
    inset: 0;
    z-index: 600;
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
  }
  .sheet-backdrop {
    position: absolute;
    inset: 0;
    border: none;
    background: rgba(0, 0, 0, 0.4);
    cursor: pointer;
  }
  .bottom-sheet {
    position: relative;
    background: var(--card);
    border-radius: 24px 24px 0 0;
    padding: 20px 20px calc(20px + env(safe-area-inset-bottom));
    box-shadow: 0 -8px 30px rgba(0, 0, 0, 0.2);
  }
  .sheet-title {
    font-size: 18px;
    font-weight: 800;
    color: var(--text);
    text-align: center;
    margin-bottom: 6px;
    word-break: break-all;
  }
  .sheet-sub {
    font-size: 14px;
    color: var(--muted);
    text-align: center;
    margin-bottom: 16px;
    word-break: break-all;
  }
  .sheet-item {
    display: block;
    width: 100%;
    min-height: 56px;
    margin-top: 8px;
    border: none;
    border-radius: 14px;
    background: var(--bg);
    font-size: 17px;
    font-weight: 700;
    color: var(--text);
    cursor: pointer;
  }
  .sheet-item:active {
    background: var(--track);
  }
  .sheet-cancel {
    color: var(--muted);
    background: none;
  }
  .sheet-actions {
    display: flex;
    gap: 12px;
  }
  .sheet-action {
    flex: 1;
    min-height: 56px;
    font-size: 17px;
  }
</style>
