<script>
  import { playAudio, stopAudio } from '../lib/audio.js';
  import { toastError } from '../lib/toast.svelte.js';

  // playing 可绑定：'blend' | 'word' | null，
  // WordCard 靠 bind:playing 得知 blend 整段播放中，驱动卡拉OK高亮的 rAF 轮询。
  let { blendUrl, wordUrl, disabled = false, playing = $bindable(null) } = $props();

  let stopHandle = null;

  function toggle(kind) {
    if (playing === kind) {
      stopAudio(); // 触发 onEnd → playing 复位
      return;
    }
    const url = kind === 'blend' ? blendUrl : wordUrl;
    playing = kind;
    stopHandle = playAudio(url, {
      onEnd: () => {
        if (playing === kind) playing = null;
      },
      onError: () => toastError('音频播放失败'),
    });
  }

  // 组件卸载（翻卡）时停掉自己发起的播放
  $effect(() => () => stopHandle?.());
</script>

<div class="play-row">
  <button
    class="btn play-btn"
    class:playing={playing === 'blend'}
    {disabled}
    onclick={() => toggle('blend')}
  >
    {playing === 'blend' ? '◼ 停止' : '▶ 拼读'}
  </button>
  <button
    class="btn play-btn"
    class:playing={playing === 'word'}
    {disabled}
    onclick={() => toggle('word')}
  >
    {playing === 'word' ? '◼ 停止' : '▶ 整词'}
  </button>
</div>

<style>
  .play-row {
    display: flex;
    gap: 12px;
    margin-top: 20px;
  }
  .play-btn {
    flex: 1;
    min-height: 64px;
    font-size: 20px;
    font-weight: 800;
    border-radius: 18px;
    background: var(--primary);
    color: #fff;
    box-shadow: 0 4px 14px rgba(240, 118, 31, 0.35);
  }
  .play-btn:active:not(:disabled) {
    transform: scale(0.97);
  }
  .play-btn.playing {
    background: var(--green);
    box-shadow: 0 4px 14px rgba(47, 169, 110, 0.35);
  }
  .play-btn:disabled {
    background: var(--track);
    color: var(--muted);
    box-shadow: none;
  }
</style>
