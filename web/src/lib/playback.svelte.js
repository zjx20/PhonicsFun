// 声明式播放层：整个应用"正在播什么"的唯一事实源。
//
// 设计约束（修一类"按钮卡在播放中"的结构性 bug）：
// 1. 组件只读 playback 状态做派生渲染（按钮文案、卡拉OK高亮、点读高亮），
//    绝不自己维护"播放中"标志；
// 2. playback 的写入者只有本模块：playFull / playSegment 开始一次会话，
//    settle 结束一次会话，没有第三种路径；
// 3. 每次播放递增代数 gen，一切异步回调（ended / error / play() 拒绝 /
//    解码完成 / rAF tick）先验代数——旧会话的任何回调都不可能影响新会话。

import { toastError } from './toast.svelte.js';

export const playback = $state({
  url: null, // 整段播放中的音频 url；null = 无整段播放
  time: 0, // 整段播放进度（毫秒），rAF 驱动，供卡拉OK高亮派生
  segment: null, // 点读中的 { url, startMs, endMs }；null = 无点读
});

let gen = 0; // 播放会话代数：每次开始/结束播放都递增，旧会话的在途回调全部失效
let el = null; // 整段播放占用的 <audio> 元素
let raf = 0;
let segSource = null; // 点读占用的 AudioBufferSourceNode
let segCtx = null; // 全局唯一 AudioContext，首次点读时在点击事件栈内创建

// settleCurrent 是唯一的"会话结束"迁移：递增代数（使该会话所有在途回调
// ——ended/error/play() 拒绝/解码完成/rAF tick——立即失效）并复位状态。
// 幂等：对已结束的状态再调用只是空转。
function settleCurrent() {
  gen++;
  cancelAnimationFrame(raf);
  if (el) {
    el.onended = el.onerror = null;
    try {
      el.pause();
    } catch {
      // 元素可能已被浏览器回收
    }
    el = null;
  }
  if (segSource) {
    segSource.onended = null;
    try {
      segSource.stop();
    } catch {
      // start 之前 stop 会抛错
    }
    segSource = null;
  }
  playback.url = null;
  playback.segment = null;
  playback.time = 0;
}

/** 停止一切播放。 */
export function stopPlayback() {
  settleCurrent();
}

function fail(g) {
  if (g !== gen) return;
  toastError('音频播放失败');
  settleCurrent();
}

/** 整段播放（blend/word 按钮）。再次调用会先结束上一次会话。 */
export function playFull(url) {
  settleCurrent();
  const g = gen; // 本会话代数（settleCurrent 刚递增过，没有任何旧回调持有它）
  playback.url = url;

  el = acquire(url);
  el.onended = () => {
    if (g === gen) settleCurrent();
  };
  el.onerror = () => fail(g);
  try {
    el.currentTime = 0;
  } catch {
    // 元数据未加载时设置 currentTime 可能抛错，播放本身不受影响
  }
  el.play().catch(() => fail(g));

  const tick = () => {
    if (g !== gen) return;
    if (el) playback.time = el.currentTime * 1000;
    raf = requestAnimationFrame(tick);
  };
  raf = requestAnimationFrame(tick);
}

/**
 * 点读：播放 url 音频的 [startMs, endMs) 毫秒片段（Web Audio 精确切片）。
 * 必须从用户点击事件处理器同步调用（iOS Safari 要求手势栈内创建/resume
 * AudioContext）。
 */
export function playSegment(url, startMs, endMs) {
  settleCurrent();
  const g = gen;
  playback.segment = { url, startMs, endMs };

  const ctx = ensureSegCtx();
  if (!ctx) {
    fail(g);
    return;
  }
  loadSegBuffer(ctx, url)
    .then((buffer) => {
      if (g !== gen) return;
      segSource = ctx.createBufferSource();
      segSource.buffer = buffer;
      segSource.connect(ctx.destination);
      segSource.onended = () => {
        if (g === gen) settleCurrent();
      };
      segSource.start(0, Math.max(0, startMs) / 1000, Math.max(0, endMs - startMs) / 1000);
    })
    .catch(() => fail(g));
}

// —— 元素与缓冲缓存 ——

const preloadCache = new Map(); // url -> Audio
const PRELOAD_LIMIT = 12;

/** 预加载一段音频（相邻卡片），重复调用同一 url 无副作用。 */
export function preload(url) {
  if (preloadCache.has(url)) return;
  const audio = new Audio();
  audio.preload = 'auto';
  audio.src = url;
  remember(url, audio);
}

function acquire(url) {
  let audio = preloadCache.get(url);
  if (!audio) {
    audio = new Audio(url);
    audio.preload = 'auto';
    remember(url, audio);
  }
  return audio;
}

function remember(url, audio) {
  preloadCache.set(url, audio);
  if (preloadCache.size <= PRELOAD_LIMIT) return;
  for (const [key, cached] of preloadCache) {
    if (preloadCache.size <= PRELOAD_LIMIT) break;
    if (cached === el) continue; // 不清正在播放的
    preloadCache.delete(key);
    cached.removeAttribute('src');
  }
}

const segBufferCache = new Map(); // url -> Promise<AudioBuffer>
const SEG_BUFFER_LIMIT = 4;

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
