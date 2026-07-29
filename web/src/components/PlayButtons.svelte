<script>
  import { playback, playFull, stopPlayback } from '../lib/playback.svelte.js';

  let { blendUrl, wordUrl, disabled = false } = $props();

  // 按钮状态纯派生自全局播放状态——没有本地"播放中"标志，不存在卡死的可能
  const blendPlaying = $derived(playback.url === blendUrl);
  const wordPlaying = $derived(playback.url === wordUrl);

  function toggle(url) {
    if (playback.url === url) {
      stopPlayback();
    } else {
      playFull(url);
    }
  }
</script>

<div class="play-row">
  <button
    class="btn play-btn"
    class:playing={blendPlaying}
    {disabled}
    onclick={() => toggle(blendUrl)}
  >
    {blendPlaying ? '◼ 停止' : '▶ 拼读'}
  </button>
  <button
    class="btn play-btn"
    class:playing={wordPlaying}
    {disabled}
    onclick={() => toggle(wordUrl)}
  >
    {wordPlaying ? '◼ 停止' : '▶ 整词'}
  </button>
</div>

<style>
  /* 位于卡片顶部操作行（WordCard .card-top），与 ⋮ 同排：flex:1 占满 ⋮ 之外的宽度 */
  .play-row {
    display: flex;
    flex: 1;
    min-width: 0;
    gap: 8px;
  }
  .play-btn {
    flex: 1;
    min-height: 46px;
    padding: 0 12px;
    font-size: 17px;
    font-weight: 800;
    border-radius: 999px;
    background: var(--primary);
    color: #fff;
    box-shadow: 0 3px 10px rgba(240, 118, 31, 0.3);
  }
  .play-btn:active:not(:disabled) {
    transform: scale(0.97);
  }
  .play-btn.playing {
    background: var(--green);
    box-shadow: 0 3px 10px rgba(47, 169, 110, 0.3);
  }
  .play-btn:disabled {
    background: var(--track);
    color: var(--muted);
    box-shadow: none;
  }
</style>
