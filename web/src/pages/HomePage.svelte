<script>
  import { groupsState, startGroupsPolling, stopGroupsPolling, removeGroup } from '../lib/groups.svelte.js';
  import GroupCard from '../components/GroupCard.svelte';

  $effect(() => {
    startGroupsPolling();
    return () => stopGroupsPolling();
  });
</script>

<header class="page-header">
  <div class="home-title-row">
    <h1 class="home-title">🎈 PhonicsFun 自然拼读</h1>
    <a class="icon-btn settings-link" href="#/settings" aria-label="设置">⚙️</a>
  </div>
  <p class="home-subtitle">一起来拼读单词吧！</p>
</header>

<main class="page">
  {#if groupsState.loading && !groupsState.loaded}
    <div class="center-hint">
      <span class="spinner"></span>
      <p>加载中…</p>
    </div>
  {:else if groupsState.error && !groupsState.loaded}
    <div class="center-hint">
      <p class="error-text">加载失败：{groupsState.error}</p>
      <button class="btn btn-primary" onclick={() => startGroupsPolling()}>重试</button>
    </div>
  {:else if groupsState.groups.length === 0}
    <div class="center-hint">
      <div class="empty-emoji">📚</div>
      <p class="empty-title">还没有单词组</p>
      <p class="muted">点右下角「＋ 导入」添加第一组单词</p>
    </div>
  {:else}
    <div class="group-list">
      {#each groupsState.groups as group (group.id)}
        <GroupCard {group} ondelete={removeGroup} />
      {/each}
    </div>
  {/if}
</main>

<a class="fab" href="#/import">＋ 导入</a>

<style>
  .home-title-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .home-title {
    font-size: 26px;
    color: var(--primary-dark);
  }
  .settings-link {
    font-size: 22px;
    text-decoration: none;
    min-width: 44px;
    min-height: 44px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .home-subtitle {
    margin-top: 4px;
    color: var(--muted);
    font-size: 15px;
  }
  .group-list {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding-bottom: 96px; /* 给悬浮按钮留空间 */
  }
  .empty-emoji {
    font-size: 56px;
  }
  .empty-title {
    font-size: 20px;
    font-weight: 700;
    margin: 8px 0 4px;
  }
  .fab {
    position: fixed;
    right: max(20px, env(safe-area-inset-right));
    bottom: max(24px, env(safe-area-inset-bottom));
    min-height: 60px;
    display: inline-flex;
    align-items: center;
    padding: 0 28px;
    border-radius: 999px;
    background: var(--primary);
    color: #fff;
    font-size: 20px;
    font-weight: 800;
    text-decoration: none;
    box-shadow: 0 8px 24px rgba(240, 118, 31, 0.4);
  }
  .fab:active {
    transform: scale(0.96);
  }
</style>
