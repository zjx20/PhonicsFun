// 音频播放：原生 Audio 整段播放 + 相邻卡预加载 + Web Audio 片段点读。
// 全局同一时刻只播一段音频：playAudio / playSegment 都会先停掉正在播的
// （无论对方是整段还是片段），并保证被停一方的 onEnd 一定被调用
// （组件靠它复位“播放中”按钮状态）。

let current = null; // { audio, finish, url }

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
  current = { audio, finish, url };

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

/** 停止当前播放（整段与片段，若有）。 */
export function stopAudio() {
  current?.finish();
  currentSegment?.finish();
}

/**
 * 当前整段播放的实时状态（供卡拉OK高亮用 rAF 轮询），无播放时返回 null。
 * 返回 { url, time（秒）, paused }。
 */
export function playbackState() {
  if (!current) return null;
  return { url: current.url, time: current.audio.currentTime, paused: current.audio.paused };
}

// —— Web Audio 片段点读 ——
// fetch 整个 blend.wav → decodeAudioData（按 url 缓存 AudioBuffer，url 带 ?v= 版本号，
// 重新生成后自动失效）→ AudioBufferSourceNode 按 cue 的毫秒区间播放。

let segCtx = null; // 全局唯一 AudioContext，首次点读时在点击事件里创建
let currentSegment = null; // { finish }
const segBufferCache = new Map(); // url -> Promise<AudioBuffer>
const SEG_BUFFER_LIMIT = 4;

// 必须在用户点击事件的同步调用栈里执行（iOS Safari 要求手势内创建/resume）。
function ensureSegCtx() {
  const Ctx = window.AudioContext || window.webkitAudioContext;
  if (!Ctx) return null;
  if (!segCtx) segCtx = new Ctx();
  if (segCtx.state === 'suspended') segCtx.resume().catch(() => {});
  return segCtx;
}

function loadSegBuffer(ctx, url) {
  let promise = segBufferCache.get(url);
  if (!promise) {
    promise = fetch(url)
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.arrayBuffer();
      })
      .then((data) => ctx.decodeAudioData(data));
    // 失败不缓存，下次点击重试
    promise.catch(() => {
      if (segBufferCache.get(url) === promise) segBufferCache.delete(url);
    });
    segBufferCache.set(url, promise);
    for (const key of segBufferCache.keys()) {
      if (segBufferCache.size <= SEG_BUFFER_LIMIT) break;
      if (key !== url) segBufferCache.delete(key);
    }
  }
  return promise;
}

/**
 * 播放 url 音频的 [startMs, endMs) 毫秒片段（点读）。
 * 约定与 playAudio 一致：返回幂等 stop 函数，onEnd 恰好调用一次。
 * 必须从用户点击事件处理器里同步调用（内部创建/resume AudioContext）。
 */
export function playSegment(url, startMs, endMs, { onEnd, onError } = {}) {
  stopAudio();
  let done = false;
  let source = null;
  const finish = () => {
    if (done) return;
    done = true;
    if (source) {
      source.onended = null;
      try {
        source.stop();
      } catch {
        // start 之前 stop 会抛错，忽略
      }
    }
    if (currentSegment === handle) currentSegment = null;
    onEnd?.();
  };
  const handle = { finish };

  const ctx = ensureSegCtx();
  if (!ctx) {
    onError?.();
    finish();
    return finish;
  }
  currentSegment = handle;

  loadSegBuffer(ctx, url)
    .then((buffer) => {
      if (done) return;
      source = ctx.createBufferSource();
      source.buffer = buffer;
      source.connect(ctx.destination);
      source.onended = finish;
      source.start(0, Math.max(0, startMs) / 1000, Math.max(0, endMs - startMs) / 1000);
    })
    .catch(() => {
      if (!done) {
        onError?.();
        finish();
      }
    });
  return finish;
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
