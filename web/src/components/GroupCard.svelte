<script>
  let { group, ondelete } = $props();

  let menuOpen = $state(false);
  let confirming = $state(false);
  let menuEl = $state(null);
  let btnEl = $state(null);

  const total = $derived(group.total ?? 0);
  const ready = $derived(group.ready ?? 0);
  const percent = $derived(total > 0 ? Math.round((Math.min(ready, total) / total) * 100) : 0);
  const allReady = $derived(total > 0 && ready >= total);

  function formatDate(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return `${d.getMonth() + 1}月${d.getDate()}日`;
  }

  function closeMenu() {
    menuOpen = false;
    confirming = false;
  }

  $effect(() => {
    if (!menuOpen) return;
    const onDocClick = (e) => {
      if (menuEl && !menuEl.contains(e.target) && btnEl && !btnEl.contains(e.target)) {
        closeMenu();
      }
    };
    document.addEventListener('click', onDocClick);
    return () => document.removeEventListener('click', onDocClick);
  });
</script>

<div class="group-card">
  <a class="group-main" href={`#/group/${encodeURIComponent(group.id)}`}>
    <div class="group-name">{group.name || '未命名单词组'}</div>
    <div class="group-meta">
      <span>{formatDate(group.createdAt)}</span>
      <span>·</span>
      <span>{total} 个单词</span>
    </div>
    <div class="progress-row">
      <div class="progress-track">
        <div class="progress-fill" class:done={allReady} style="width: {percent}%"></div>
      </div>
      <span class="progress-text" class:done={allReady}>
        {allReady ? '全部就绪 ✓' : `就绪 ${ready}/${total}`}
      </span>
    </div>
  </a>

  <button
    bind:this={btnEl}
    class="icon-btn menu-btn"
    aria-label="更多操作"
    onclick={() => {
      confirming = false;
      menuOpen = !menuOpen;
    }}
  >⋮</button>

  {#if menuOpen}
    <div class="pop-menu" bind:this={menuEl}>
      {#if confirming}
        <p class="confirm-text">确认删除「{group.name || '未命名单词组'}」？</p>
        <div class="confirm-actions">
          <button
            class="btn btn-danger btn-small"
            onclick={() => {
              closeMenu();
              ondelete(group.id);
            }}
          >删除</button>
          <button class="btn btn-ghost btn-small" onclick={closeMenu}>取消</button>
        </div>
      {:else}
        <button class="menu-item" onclick={() => (confirming = true)}>🗑 删除</button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .group-card {
    position: relative;
    background: var(--card);
    border-radius: 20px;
    box-shadow: 0 2px 10px rgba(90, 70, 40, 0.08);
  }
  .group-main {
    display: block;
    padding: 18px 56px 18px 20px;
    text-decoration: none;
    color: inherit;
    min-height: 44px;
  }
  .group-name {
    font-size: 20px;
    font-weight: 800;
    color: var(--text);
    word-break: break-word;
  }
  .group-meta {
    display: flex;
    gap: 6px;
    margin-top: 4px;
    color: var(--muted);
    font-size: 14px;
  }
  .progress-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 12px;
  }
  .progress-track {
    flex: 1;
    height: 10px;
    border-radius: 999px;
    background: var(--track);
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    border-radius: 999px;
    background: var(--primary);
    transition: width 0.4s ease;
  }
  .progress-fill.done {
    background: var(--green);
  }
  .progress-text {
    flex-shrink: 0;
    font-size: 13px;
    font-weight: 700;
    color: var(--muted);
  }
  .progress-text.done {
    color: var(--green);
  }
  .menu-btn {
    position: absolute;
    top: 10px;
    right: 8px;
  }
  .pop-menu {
    position: absolute;
    top: 52px;
    right: 12px;
    z-index: 20;
    background: var(--card);
    border-radius: 14px;
    box-shadow: 0 8px 28px rgba(0, 0, 0, 0.18);
    padding: 8px;
    min-width: 180px;
  }
  .menu-item {
    display: block;
    width: 100%;
    min-height: 48px;
    padding: 0 16px;
    border: none;
    background: none;
    border-radius: 10px;
    font-size: 16px;
    font-weight: 600;
    color: var(--red);
    text-align: left;
    cursor: pointer;
  }
  .menu-item:active {
    background: var(--track);
  }
  .confirm-text {
    padding: 8px 8px 10px;
    font-size: 14px;
    color: var(--text);
    word-break: break-word;
  }
  .confirm-actions {
    display: flex;
    gap: 8px;
    padding: 0 4px 4px;
  }
</style>
