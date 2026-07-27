<script>
  import { extractFromText, extractFromImage, createGroup } from '../lib/api.js';
  import { toastError, toastSuccess } from '../lib/toast.svelte.js';
  import WordChip from '../components/WordChip.svelte';

  // 状态机：input（输入）→ extracting（提取中）→ pick（勾选）→ creating（创建中）
  let step = $state('input');
  let mode = $state('text'); // text | image
  let text = $state('');
  let imageFile = $state(null);
  let imagePreview = $state('');
  let picks = $state([]); // [{word, removed}]
  let groupName = $state('');
  let error = $state('');
  let controller = null;

  const keptCount = $derived(picks.filter((p) => !p.removed).length);
  const canExtract = $derived(mode === 'text' ? text.trim().length > 0 : imageFile !== null);

  function defaultGroupName() {
    const now = new Date();
    return `${now.getMonth() + 1}月${now.getDate()}日 单词组`;
  }

  function onFileChange(event) {
    const file = event.target.files?.[0] ?? null;
    imageFile = file;
    if (imagePreview) URL.revokeObjectURL(imagePreview);
    imagePreview = file ? URL.createObjectURL(file) : '';
  }

  $effect(() => () => {
    if (imagePreview) URL.revokeObjectURL(imagePreview);
    controller?.abort();
  });

  function loadImageElement(file) {
    return new Promise((resolve, reject) => {
      const url = URL.createObjectURL(file);
      const img = new Image();
      img.onload = () => resolve({ img, url });
      img.onerror = () => {
        URL.revokeObjectURL(url);
        reject(new Error('无法读取该图片，请换一张试试'));
      };
      img.src = url;
    });
  }

  // 上传前用 canvas 压缩：长边 ≤1600px 的 JPEG（quality 0.85），省流量也加快识别
  async function compressImage(file) {
    const { img, url } = await loadImageElement(file);
    try {
      const maxSide = 1600;
      const scale = Math.min(1, maxSide / Math.max(img.naturalWidth, img.naturalHeight));
      const width = Math.max(1, Math.round(img.naturalWidth * scale));
      const height = Math.max(1, Math.round(img.naturalHeight * scale));
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      canvas.getContext('2d').drawImage(img, 0, 0, width, height);
      const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.85));
      if (!blob) throw new Error('图片处理失败，请换一张试试');
      return blob;
    } finally {
      URL.revokeObjectURL(url);
    }
  }

  async function startExtract() {
    if (!canExtract) return;
    error = '';
    step = 'extracting';
    controller = new AbortController();
    try {
      let result;
      if (mode === 'text') {
        result = await extractFromText(text.trim(), controller.signal);
      } else {
        const blob = await compressImage(imageFile);
        result = await extractFromImage(blob, controller.signal);
      }
      const words = result?.words ?? [];
      if (words.length === 0) {
        step = 'input';
        error = '没有识别到任何单词，请检查输入内容后重试';
        return;
      }
      picks = words.map((word) => ({ word, removed: false }));
      groupName = defaultGroupName();
      step = 'pick';
    } catch (err) {
      step = 'input'; // 失败保留输入
      if (err.name === 'AbortError') return;
      error = err.message;
      toastError(err.message);
    } finally {
      controller = null;
    }
  }

  function cancelExtract() {
    controller?.abort();
  }

  async function submitGroup() {
    if (keptCount === 0) return;
    step = 'creating';
    try {
      const words = picks.filter((p) => !p.removed).map((p) => p.word);
      const { id } = await createGroup({ name: groupName.trim() || defaultGroupName(), words });
      toastSuccess('单词组创建成功');
      location.hash = `#/group/${encodeURIComponent(id)}`;
    } catch (err) {
      toastError(err.message);
      step = 'pick';
    }
  }
</script>

<header class="page-header with-back">
  <a class="back-btn" href="#/">‹ 返回</a>
  <h1>导入单词</h1>
</header>

<main class="page">
  {#if step === 'input' || step === 'extracting'}
    <div class="tab-row" role="tablist">
      <button
        class="tab-btn"
        class:active={mode === 'text'}
        role="tab"
        aria-selected={mode === 'text'}
        onclick={() => (mode = 'text')}
      >📝 粘贴文本</button>
      <button
        class="tab-btn"
        class:active={mode === 'image'}
        role="tab"
        aria-selected={mode === 'image'}
        onclick={() => (mode = 'image')}
      >📷 拍照选图</button>
    </div>

    {#if error}
      <div class="error-banner">
        <span>{error}</span>
        <button class="btn btn-small btn-warn" disabled={!canExtract} onclick={startExtract}>重试</button>
      </div>
    {/if}

    {#if mode === 'text'}
      <textarea
        class="text-input"
        rows="8"
        placeholder="把含有英文单词的内容粘贴到这里，比如课本上的一段话、单词表…"
        bind:value={text}
      ></textarea>
    {:else}
      <label class="file-drop">
        <input type="file" accept="image/*" capture="environment" onchange={onFileChange} />
        {#if imagePreview}
          <img class="file-preview" src={imagePreview} alt="所选图片预览" />
          <span class="file-hint">点击可重新拍照 / 换图</span>
        {:else}
          <span class="file-emoji">📷</span>
          <span class="file-hint-big">拍下课本或单词表</span>
          <span class="file-hint">点击拍照，或从相册选择</span>
        {/if}
      </label>
    {/if}

    <button class="btn btn-primary btn-big extract-btn" disabled={!canExtract} onclick={startExtract}>
      ✨ 提取单词
    </button>
  {:else}
    <div class="pick-panel">
      <div class="pick-header">
        <span class="pick-count">将导入 <b>{keptCount}</b> 个单词</span>
        <button class="btn btn-ghost btn-small" disabled={step === 'creating'} onclick={() => (step = 'input')}>
          返回修改
        </button>
      </div>
      <p class="pick-tip">点一下单词可划除，再点一下恢复</p>
      <div class="chip-grid">
        {#each picks as pick, i (i)}
          <WordChip word={pick.word} removed={pick.removed} ontoggle={() => (pick.removed = !pick.removed)} />
        {/each}
      </div>
      <label class="name-field">
        <span>组名称</span>
        <input type="text" bind:value={groupName} placeholder={defaultGroupName()} maxlength="40" />
      </label>
      <button
        class="btn btn-primary btn-big"
        disabled={keptCount === 0 || step === 'creating'}
        onclick={submitGroup}
      >
        {step === 'creating' ? '创建中…' : `✓ 创建单词组（${keptCount} 个）`}
      </button>
    </div>
  {/if}
</main>

{#if step === 'extracting'}
  <div class="overlay">
    <span class="spinner spinner-big"></span>
    <p class="overlay-text">{mode === 'text' ? '正在提取单词…' : '正在识别图片中的单词…'}</p>
    <button class="btn btn-ghost overlay-cancel" onclick={cancelExtract}>取消</button>
  </div>
{/if}

<style>
  .tab-row {
    display: flex;
    gap: 10px;
    margin-bottom: 16px;
  }
  .tab-btn {
    flex: 1;
    min-height: 52px;
    border: 2px solid transparent;
    border-radius: 16px;
    background: var(--card);
    font-size: 17px;
    font-weight: 700;
    color: var(--muted);
    cursor: pointer;
  }
  .tab-btn.active {
    border-color: var(--primary);
    color: var(--primary-dark);
    background: var(--primary-soft);
  }
  .text-input {
    width: 100%;
    border: 2px solid var(--track);
    border-radius: 16px;
    background: var(--card);
    padding: 14px;
    font-size: 17px;
    font-family: inherit;
    color: var(--text);
    resize: vertical;
    min-height: 180px;
  }
  .text-input:focus {
    outline: none;
    border-color: var(--primary);
  }
  .file-drop {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 220px;
    border: 2px dashed var(--primary);
    border-radius: 20px;
    background: var(--card);
    padding: 20px;
    cursor: pointer;
    text-align: center;
  }
  .file-drop input {
    display: none;
  }
  .file-emoji {
    font-size: 48px;
  }
  .file-hint-big {
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
  }
  .file-hint {
    font-size: 14px;
    color: var(--muted);
  }
  .file-preview {
    max-width: 100%;
    max-height: 260px;
    border-radius: 12px;
    object-fit: contain;
  }
  .extract-btn {
    margin-top: 18px;
    width: 100%;
  }
  .error-banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    background: var(--red-soft);
    color: var(--red);
    border-radius: 14px;
    padding: 12px 14px;
    margin-bottom: 14px;
    font-size: 15px;
    font-weight: 600;
  }
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 500;
    background: rgba(255, 250, 240, 0.94);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 18px;
  }
  .overlay-text {
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
  }
  .overlay-cancel {
    min-width: 120px;
  }
  .pick-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }
  .pick-count {
    font-size: 18px;
    color: var(--text);
  }
  .pick-count b {
    color: var(--primary-dark);
    font-size: 22px;
  }
  .pick-tip {
    margin: 8px 0 14px;
    font-size: 14px;
    color: var(--muted);
  }
  .chip-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 20px;
  }
  .name-field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 18px;
  }
  .name-field span {
    font-size: 14px;
    font-weight: 700;
    color: var(--muted);
  }
  .name-field input {
    min-height: 48px;
    border: 2px solid var(--track);
    border-radius: 14px;
    background: var(--card);
    padding: 0 14px;
    font-size: 17px;
    font-family: inherit;
    color: var(--text);
  }
  .name-field input:focus {
    outline: none;
    border-color: var(--primary);
  }
  .pick-panel > .btn-big {
    width: 100%;
  }
</style>
