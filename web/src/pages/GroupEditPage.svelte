<script>
  // 单词列表 + 编辑页：总览组内全部单词与生成状态，可改词、增删词、拖拽调序、
  // 改组名。进入时加载一次组详情，不轮询——避免刷新覆盖正在编辑的输入，
  // 状态徽标取加载时快照即可。全部改动都在内存里，点「保存」才提交
  // PUT /api/groups/{id}（words 顺序即翻卡顺序），返回播放页由那边的轮询
  // 跟踪新词生成。
  import { flip } from 'svelte/animate';
  import { getGroup, updateGroup } from '../lib/api.js';
  import { toastError, toastSuccess } from '../lib/toast.svelte.js';

  let { id } = $props();

  let loading = $state(true);
  let loadError = $state('');
  let name = $state('');
  // 行：word 当前文本；original 加载时的原词（null = 本次新增）；
  // slug/textState/audioState 是加载时快照；removed = 划除待删。
  let rows = $state([]);
  let newWords = $state('');
  let saving = $state(false);
  let editingKey = $state(null);
  let editingBackup = '';
  let nextKey = 0;

  // 与后端 store.Slug 保持一致：小写、仅保留 [a-z0-9']、' → _。
  // 前端用它判断"改动是否会触发重新生成"（slug 变才算改词）与去重。
  function slugOf(word) {
    return (word || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9']/g, '')
      .replace(/'/g, '_');
  }

  const isChanged = (row) => row.original === null || slugOf(row.word) !== slugOf(row.original);
  const keptCount = $derived(rows.filter((r) => !r.removed && slugOf(r.word)).length);

  async function load() {
    loading = true;
    loadError = '';
    try {
      const detail = await getGroup(id);
      name = detail.name || '';
      rows = (detail.words ?? []).map((w) => ({
        key: nextKey++,
        word: w.word,
        original: w.word,
        slug: w.slug,
        textState: w.text,
        audioState: w.audio,
        removed: false,
      }));
    } catch (err) {
      loadError = err.message;
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void id;
    load();
  });

  function badge(row) {
    if (isChanged(row)) return { text: '保存后生成', cls: 'new' };
    if (row.textState === 'failed' || row.audioState === 'failed') return { text: '✗ 失败', cls: 'failed' };
    if (row.textState === 'done' && row.audioState === 'done') return { text: '✓ 就绪', cls: 'ready' };
    return { text: '⏳ 生成中', cls: 'pending' };
  }

  // —— 行内编辑 ——

  function startEdit(row) {
    if (row.removed) return;
    editingBackup = row.word;
    editingKey = row.key;
  }

  function finishEdit(row) {
    if (editingKey !== row.key) return; // Escape 已退出时，随后的 blur 不再处理
    editingKey = null;
    const w = row.word.trim();
    if (!w) {
      if (row.original === null) {
        rows = rows.filter((r) => r.key !== row.key); // 新增行清空 = 撤掉这一行
      } else {
        row.word = row.original; // 已有行清空 = 恢复原词
      }
      return;
    }
    row.word = w;
  }

  function onEditKey(e, row) {
    if (e.key === 'Enter') {
      e.preventDefault();
      e.currentTarget.blur(); // 触发 finishEdit
    } else if (e.key === 'Escape') {
      row.word = editingBackup;
      editingKey = null;
    }
  }

  function focusInput(node) {
    node.focus();
    node.select();
  }

  // 点词：未改动的词直达播放页对应卡片；改过/新增的词还没有卡片，转为继续编辑
  function onWordClick(row) {
    if (isChanged(row)) {
      startEdit(row);
      return;
    }
    location.hash = `#/group/${encodeURIComponent(id)}?word=${encodeURIComponent(row.slug || slugOf(row.word))}`;
  }

  // —— 拖拽调序（手写 pointer 事件，与 PlayerPage 翻卡手势同风格，零依赖）——
  // 把手 pointerdown 后 setPointerCapture，move 时按垂直位移换算目标下标、
  // 实时 splice 重排（flip 补间动画）；每次重排后平移基准 Y，保证继续拖动
  // 时的位移始终相对当前位置计算。
  let dragKey = $state(null);
  let dragIndex = 0;
  let dragStartY = 0;
  let rowStride = 0;

  function onHandleDown(e, index) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    e.preventDefault();
    const rowEl = e.currentTarget.closest('.word-row');
    const next = rowEl?.nextElementSibling;
    // 行距用相邻行 offsetTop 差（含 gap）；只有一行时没得拖，随便给个值
    rowStride = next ? next.offsetTop - rowEl.offsetTop : (rowEl?.offsetHeight ?? 56);
    dragKey = rows[index].key;
    dragIndex = index;
    dragStartY = e.clientY;
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function onHandleMove(e) {
    if (dragKey === null || rowStride <= 0) return;
    const offset = Math.round((e.clientY - dragStartY) / rowStride);
    const target = Math.max(0, Math.min(rows.length - 1, dragIndex + offset));
    if (target === dragIndex) return;
    const [moved] = rows.splice(dragIndex, 1);
    rows.splice(target, 0, moved);
    dragStartY += (target - dragIndex) * rowStride;
    dragIndex = target;
  }

  function onHandleUp() {
    dragKey = null;
  }

  // —— 添加 ——

  function addWords() {
    const parts = newWords
      .split(/[,，、\s]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    let dup = 0;
    let invalid = 0;
    for (const p of parts) {
      const slug = slugOf(p);
      if (!slug) {
        invalid++;
        continue;
      }
      const existing = rows.find((r) => slugOf(r.word) === slug);
      if (existing) {
        if (existing.removed) {
          existing.removed = false; // 加回一个刚划除的词 = 恢复那一行
        } else {
          dup++;
        }
        continue;
      }
      rows.push({ key: nextKey++, word: p, original: null, slug: '', textState: '', audioState: '', removed: false });
    }
    newWords = '';
    if (dup > 0 || invalid > 0) {
      const skipped = [];
      if (dup > 0) skipped.push(`${dup} 个已在列表中`);
      if (invalid > 0) skipped.push(`${invalid} 个不是英文单词`);
      toastError(`已跳过：${skipped.join('，')}`);
    }
  }

  // —— 保存 ——

  async function save() {
    if (saving) return;
    const words = [];
    const seen = new Set();
    for (const r of rows) {
      if (r.removed) continue;
      const w = r.word.trim();
      const slug = slugOf(w);
      if (!w || !slug || seen.has(slug)) continue;
      seen.add(slug);
      words.push(w);
    }
    if (words.length === 0) return;
    saving = true;
    try {
      await updateGroup(id, { name: name.trim(), words });
      toastSuccess('已保存修改');
      location.hash = `#/group/${encodeURIComponent(id)}`;
    } catch (err) {
      toastError(err.message);
    } finally {
      saving = false;
    }
  }
</script>

<header class="page-header with-back">
  <a class="back-btn" href={`#/group/${encodeURIComponent(id)}`}>‹ 返回</a>
  <h1>单词列表</h1>
</header>

<main class="page">
  {#if loading}
    <div class="center-hint">
      <span class="spinner"></span>
      <p>加载中…</p>
    </div>
  {:else if loadError}
    <div class="center-hint">
      <p class="error-text">{loadError}</p>
      <button class="btn btn-primary" onclick={load}>重试</button>
    </div>
  {:else}
    <label class="name-field">
      <span>组名称</span>
      <input type="text" bind:value={name} maxlength="40" />
    </label>

    <p class="list-tip">点单词直接看卡片；✎ 改拼写，✕ 移除（再点恢复），按住 ≡ 拖动调顺序</p>

    <div class="word-list">
      {#each rows as row, i (row.key)}
        <div
          class="word-row"
          class:removed={row.removed}
          class:dragging={dragKey === row.key}
          animate:flip={{ duration: 150 }}
        >
          <button
            class="row-handle"
            aria-label="拖动调整顺序"
            onpointerdown={(e) => onHandleDown(e, i)}
            onpointermove={onHandleMove}
            onpointerup={onHandleUp}
            onpointercancel={onHandleUp}
          >≡</button>
          {#if editingKey === row.key}
            <input
              class="row-input"
              type="text"
              bind:value={row.word}
              use:focusInput
              onblur={() => finishEdit(row)}
              onkeydown={(e) => onEditKey(e, row)}
            />
          {:else}
            {@const b = badge(row)}
            <button class="row-word" onclick={() => onWordClick(row)} disabled={row.removed}>
              <span class="row-text">{row.word}</span>
              {#if !row.removed && !isChanged(row)}
                <!-- 「可进入」暗示：点词直达播放页这张卡；改过/新增的词点击是继续编辑，不给箭头 -->
                <span class="row-go">›</span>
              {/if}
            </button>
            {#if !row.removed}
              <span class="row-badge {b.cls}">{b.text}</span>
            {/if}
            <button class="icon-btn row-btn" aria-label="修改拼写" onclick={() => startEdit(row)} disabled={row.removed}>
              ✎
            </button>
            <button
              class="icon-btn row-btn"
              aria-label={row.removed ? '恢复' : '移除'}
              onclick={() => (row.removed = !row.removed)}
            >{row.removed ? '↩' : '✕'}</button>
          {/if}
        </div>
      {/each}
    </div>

    <div class="add-row">
      <input
        type="text"
        bind:value={newWords}
        placeholder="添加单词，空格分隔可一次加多个"
        onkeydown={(e) => e.key === 'Enter' && addWords()}
      />
      <button class="btn" onclick={addWords} disabled={!newWords.trim()}>添加</button>
    </div>

    <div class="save-bar">
      <button class="btn btn-primary btn-big save-btn" disabled={keptCount === 0 || saving} onclick={save}>
        {saving ? '保存中…' : `✓ 保存（${keptCount} 个单词）`}
      </button>
    </div>
  {/if}
</main>

<style>
  .name-field {
    display: flex;
    flex-direction: column;
    gap: 6px;
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
  .list-tip {
    margin: 14px 0 10px;
    font-size: 13px;
    color: var(--muted);
  }
  .word-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .word-row {
    display: flex;
    align-items: center;
    gap: 4px;
    background: var(--card);
    border-radius: 14px;
    padding: 4px 6px 4px 0;
    box-shadow: 0 1px 4px rgba(90, 70, 40, 0.06);
  }
  .word-row.dragging {
    position: relative;
    z-index: 5;
    box-shadow: 0 8px 20px rgba(90, 70, 40, 0.25);
    transform: scale(1.02);
  }
  .word-row.removed {
    opacity: 0.55;
  }
  .row-handle {
    flex-shrink: 0;
    width: 44px;
    min-height: 48px;
    border: none;
    background: none;
    color: var(--dot);
    font-size: 20px;
    cursor: grab;
    touch-action: none; /* 拖拽的生命线：阻止移动端把 pointermove 当作页面滚动 */
  }
  .row-word {
    flex: 1;
    min-width: 0;
    min-height: 48px;
    display: flex;
    align-items: center;
    gap: 6px;
    border: none;
    background: none;
    padding: 0 6px 0 0;
    border-radius: 10px;
    font-family: inherit;
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  .row-word:active:not(:disabled) {
    background: var(--primary-soft);
  }
  .row-go {
    flex-shrink: 0;
    color: var(--primary);
    font-size: 22px;
    font-weight: 800;
    line-height: 1;
    padding-bottom: 2px; /* › 字形偏高，微调视觉居中 */
  }
  .word-row.removed .row-word {
    text-decoration: line-through;
    text-decoration-thickness: 2px;
    color: var(--muted);
    cursor: default;
  }
  .row-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .row-input {
    flex: 1;
    min-width: 0;
    min-height: 44px;
    margin: 2px 2px 2px 0;
    border: 2px solid var(--primary);
    border-radius: 10px;
    padding: 0 10px;
    font-family: inherit;
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
    background: var(--card);
  }
  .row-input:focus {
    outline: none;
  }
  .row-badge {
    flex-shrink: 0;
    font-size: 12px;
    font-weight: 700;
    padding: 3px 8px;
    border-radius: 999px;
  }
  .row-badge.ready {
    color: var(--green);
  }
  .row-badge.pending {
    color: var(--muted);
  }
  .row-badge.failed {
    color: var(--red);
  }
  .row-badge.new {
    color: var(--primary-dark);
    background: var(--primary-soft);
  }
  .row-btn {
    flex-shrink: 0;
    font-size: 18px;
  }
  .row-btn:disabled {
    opacity: 0.35;
    cursor: default;
  }
  .add-row {
    display: flex;
    gap: 8px;
    margin-top: 14px;
  }
  .add-row input {
    flex: 1;
    min-width: 0;
    min-height: 48px;
    border: 2px solid var(--track);
    border-radius: 14px;
    background: var(--card);
    padding: 0 14px;
    font-size: 17px;
    font-family: inherit;
    color: var(--text);
  }
  .add-row input:focus {
    outline: none;
    border-color: var(--primary);
  }
  .save-bar {
    position: sticky;
    bottom: 0;
    margin-top: 16px;
    padding: 10px 0 calc(10px + env(safe-area-inset-bottom));
    background: linear-gradient(transparent, var(--bg) 35%);
  }
  .save-btn {
    width: 100%;
  }
</style>
