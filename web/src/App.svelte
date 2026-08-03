<script>
  import HomePage from './pages/HomePage.svelte';
  import ImportWizard from './pages/ImportWizard.svelte';
  import PlayerPage from './pages/PlayerPage.svelte';
  import GroupEditPage from './pages/GroupEditPage.svelte';
  import SettingsPage from './pages/SettingsPage.svelte';
  import Toast from './components/Toast.svelte';
  import TeacherFab from './components/TeacherFab.svelte';

  // 手写 hash 路由：#/ 首页、#/import 导入、#/group/{id} 播放页（可带
  // ?word={slug} 指定起始卡片）、#/group/{id}/edit 单词列表/编辑页、
  // #/settings 设置页，未知路径回首页。
  function parseHash() {
    const hash = (location.hash || '').replace(/^#/, '');
    if (hash === '' || hash === '/') return { name: 'home' };
    if (hash === '/import') return { name: 'import' };
    if (hash === '/settings') return { name: 'settings' };
    const edit = hash.match(/^\/group\/([^/?]+)\/edit$/);
    if (edit) return { name: 'group-edit', id: decodeURIComponent(edit[1]) };
    const group = hash.match(/^\/group\/([^/?]+)(?:\?(.*))?$/);
    if (group) {
      const params = new URLSearchParams(group[2] || '');
      return { name: 'group', id: decodeURIComponent(group[1]), word: params.get('word') || '' };
    }
    return { name: 'home' };
  }

  let route = $state(parseHash());

  $effect(() => {
    const onHashChange = () => {
      route = parseHash();
    };
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  });
</script>

{#if route.name === 'home'}
  <HomePage />
{:else if route.name === 'import'}
  <ImportWizard />
{:else if route.name === 'group'}
  <PlayerPage id={route.id} word={route.word} />
{:else if route.name === 'group-edit'}
  <GroupEditPage id={route.id} />
{:else if route.name === 'settings'}
  <SettingsPage />
{/if}

<!-- AI 老师浮动按钮跨路由常驻：WS 连接不随页面切换重建 -->
<TeacherFab />
<Toast />
