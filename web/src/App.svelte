<script>
  import HomePage from './pages/HomePage.svelte';
  import ImportWizard from './pages/ImportWizard.svelte';
  import PlayerPage from './pages/PlayerPage.svelte';
  import Toast from './components/Toast.svelte';

  // 手写 hash 路由：#/ 首页、#/import 导入、#/group/{id} 播放页，未知路径回首页。
  function parseHash() {
    const hash = (location.hash || '').replace(/^#/, '');
    if (hash === '' || hash === '/') return { name: 'home' };
    if (hash === '/import') return { name: 'import' };
    const group = hash.match(/^\/group\/([^/]+)$/);
    if (group) return { name: 'group', id: decodeURIComponent(group[1]) };
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
  <PlayerPage id={route.id} />
{/if}

<Toast />
