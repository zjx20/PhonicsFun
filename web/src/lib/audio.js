// 原生 Audio 播放 + 相邻卡预加载。
// 全局同一时刻只播一段音频：playAudio 会先停掉正在播的那段，
// 并保证被停一方的 onEnd 一定被调用（组件靠它复位“播放中”按钮状态）。

let current = null; // { audio, finish }

const preloadCache = new Map(); // url -> Audio
const PRELOAD_LIMIT = 12;

/**
 * 播放 url，返回一个幂等的 stop 函数。
 * onEnd 在播放自然结束、被停止或出错时都会调用（恰好一次）。
 */
export function playAudio(url, { onEnd, onError } = {}) {
  stopAudio();
  let audio = preloadCache.get(url);
  if (!audio) {
    audio = new Audio(url);
    audio.preload = 'auto';
    remember(url, audio);
  }

  let done = false;
  const finish = () => {
    if (done) return;
    done = true;
    audio.removeEventListener('ended', finish);
    audio.removeEventListener('error', onErrorEvent);
    try {
      audio.pause();
    } catch {
      // 忽略：元素可能已被浏览器回收
    }
    if (current && current.audio === audio) current = null;
    onEnd?.();
  };
  const onErrorEvent = () => {
    if (!done) {
      onError?.();
      finish();
    }
  };

  audio.addEventListener('ended', finish);
  audio.addEventListener('error', onErrorEvent);
  current = { audio, finish };

  try {
    audio.currentTime = 0;
  } catch {
    // 尚未加载元数据时设置 currentTime 可能抛错，忽略
  }
  audio.play().catch(() => {
    if (!done) {
      onError?.();
      finish();
    }
  });
  return finish;
}

/** 停止当前播放（若有）。 */
export function stopAudio() {
  current?.finish();
}

/** 预加载一段音频（用于相邻卡片），重复调用同一 url 无副作用。 */
export function preloadAudio(url) {
  if (preloadCache.has(url)) return;
  const audio = new Audio();
  audio.preload = 'auto';
  audio.src = url;
  remember(url, audio);
}

function remember(url, audio) {
  preloadCache.set(url, audio);
  if (preloadCache.size <= PRELOAD_LIMIT) return;
  for (const [key, cached] of preloadCache) {
    if (preloadCache.size <= PRELOAD_LIMIT) break;
    if (current && current.audio === cached) continue;
    preloadCache.delete(key);
    cached.removeAttribute('src');
  }
}
