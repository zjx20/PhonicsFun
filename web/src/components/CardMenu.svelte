<script>
  // 卡片右上 ⋮ 的 bottom-sheet：先选目标，再二次确认，防手滑触发 AI 调用。
  // 确认步骤可附纠错反馈（如"音标不对"），注入文本生成 prompt；音频脚本
  // 是机械拼装的、不接受自由文本，所以 target=audio 无反馈框，改提示用户
  // 内容/发音标注问题应走文本重生成（自动级联重生音频）。
  let { open = false, word = '', onclose, onconfirm } = $props();

  let chosen = $state(null); // null | 'text' | 'audio' | 'both'
  let feedback = $state('');

  $effect(() => {
    if (!open) {
      chosen = null;
      feedback = '';
    }
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
        {#if chosen === 'audio'}
          <p class="sheet-hint">音频会按当前卡片内容重新录制。若是拼读拆解、注音这类内容问题，请选「重新生成文本」并写明哪里不对（音频会跟着重新生成）。</p>
        {:else}
          <textarea
            class="feedback-input"
            rows="3"
            maxlength="500"
            placeholder="可选：告诉 AI 哪里不对，如“音标不对，重音应在第一音节”“例句太难”"
            bind:value={feedback}
          ></textarea>
        {/if}
        <div class="sheet-actions">
          <button
            class="btn btn-danger sheet-action"
            onclick={() => onconfirm(chosen, chosen === 'audio' ? '' : feedback.trim())}
            >确认</button
          >
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
  .sheet-hint {
    margin-bottom: 16px;
    padding: 10px 12px;
    border-radius: 12px;
    background: var(--bg);
    font-size: 13px;
    line-height: 1.5;
    color: var(--muted);
  }
  .feedback-input {
    display: block;
    width: 100%;
    margin-bottom: 16px;
    padding: 10px 12px;
    border: 1.5px solid var(--track);
    border-radius: 12px;
    background: var(--bg);
    font-family: inherit;
    font-size: 15px;
    line-height: 1.5;
    color: var(--text);
    resize: none;
  }
  .feedback-input:focus {
    outline: none;
    border-color: var(--primary);
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
