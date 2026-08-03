// AI 老师：一条常驻 WebSocket（/api/teacher/live）上的实时语音对话。
//
// 结构照搬 playback.svelte.js 的三条设计约束：组件只读 teacherState 做派生
// 渲染；状态写入者只有本模块；每次 start/stop 递增代数 gen，旧会话的一切
// 异步回调（ws 事件、worklet 消息、source.onended、getUserMedia 完成）先验
// 代数，绝不影响新会话。
//
// 帧协议（与 internal/httpapi/teacher.go 顶部注释一致）：
//   上行二进制 = 16kHz PCM16 麦克风帧；上行 JSON = context/card/mic；
//   下行二进制 = 24kHz PCM16 老师语音；下行 JSON = ready/transcript/
//   interrupted/turn_complete/restarted/error。
//
// 与本地播放层互斥：老师启动时 stopPlayback()；本地在播（卡片音频）期间
// 暂停上行麦克风帧并发一次 {"type":"mic","on":false}，避免老师把卡片音频
// 听成学生发音；本地播放结束后自动恢复。

import { BASE } from './api.js';
import { playback, stopPlayback } from './playback.svelte.js';
import { playerState } from './player.svelte.js';
import { toastError } from './toast.svelte.js';

const DOWN_SAMPLE_RATE = 24000; // 下行 PCM 采样率（后端 llm.LiveSampleRate）
const TRANSCRIPT_LIMIT = 20;
const CARD_DEBOUNCE_MS = 800; // 连续翻卡只注入最终停留的卡

export const teacherState = $state({
  status: 'idle', // idle | connecting | listening | speaking | error
  error: '',
  transcript: [], // [{role:'user'|'teacher', text}]，最近 TRANSCRIPT_LIMIT 条
  // 局域网 http 部署时 getUserMedia 不存在（需 HTTPS 或 localhost）
  supported:
    typeof window !== 'undefined' &&
    window.isSecureContext &&
    !!navigator.mediaDevices?.getUserMedia,
});

let gen = 0; // 会话代数
let ws = null;
let ctx = null; // 老师专用 AudioContext（采集 + 下行播放共用一个）
let micStream = null;
let micNode = null;
let micSrcNode = null;
let micMuteGain = null;
let micPausedForPlayback = false;
let nextT = 0; // 下行播放队列的排队时刻
const activeSources = new Set();
let lastSentGroupId = null;
let lastSentSlug = null;
let cardDebounce = 0;
let workletBlobUrl = null;

// 采集 worklet：128 帧配额里线性插值降采样到 16k，攒 ~40ms（640 样本）再
// postMessage，主线程直接 ws.send。以内联字符串 + Blob URL 加载，绕开
// Vite 资产路径（base:'./'）并满足零外链约定。
const WORKLET_SRC = `
class PhonicsMicProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.ratio = sampleRate / 16000;
    this.t = 0;       // 相对当前块首样本的小数读取位置（可为 [-1,0)，指向上一块末样本）
    this.prev = 0;    // 上一块最后一个样本，跨块插值用
    this.out = new Int16Array(640);
    this.n = 0;
  }
  process(inputs) {
    const input = inputs[0] && inputs[0][0];
    if (!input || input.length === 0) return true;
    const len = input.length;
    let t = this.t;
    while (t < len - 1) {
      const i = Math.floor(t);
      const frac = t - i;
      const a = i < 0 ? this.prev : input[i];
      const b = input[i + 1];
      let v = a + (b - a) * frac;
      v = v < -1 ? -1 : v > 1 ? 1 : v;
      this.out[this.n++] = v < 0 ? v * 0x8000 : v * 0x7fff;
      if (this.n === this.out.length) {
        this.port.postMessage(this.out.buffer.slice(0));
        this.n = 0;
      }
      t += this.ratio;
    }
    this.t = t - len;
    this.prev = input[len - 1];
    return true;
  }
}
registerProcessor('phonics-mic', PhonicsMicProcessor);
`;

function workletUrl() {
  if (!workletBlobUrl) {
    workletBlobUrl = URL.createObjectURL(new Blob([WORKLET_SRC], { type: 'text/javascript' }));
  }
  return workletBlobUrl;
}

/**
 * 启动 AI 老师。必须从用户点击事件处理器同步调用（iOS Safari 要求手势栈
 * 内创建/resume AudioContext；getUserMedia 的权限弹窗也应贴着手势）。
 */
export function startTeacher() {
  if (teacherState.status !== 'idle' && teacherState.status !== 'error') return;
  if (!teacherState.supported) {
    toastError('AI 老师需要 HTTPS（或 localhost）访问才能使用麦克风');
    return;
  }
  teardown();
  const g = ++gen;
  teacherState.status = 'connecting';
  teacherState.error = '';
  teacherState.transcript = [];
  lastSentGroupId = null;
  lastSentSlug = null;
  stopPlayback();

  // AudioContext 必须在手势栈内同步创建
  const Ctx = window.AudioContext || window.webkitAudioContext;
  ctx = new Ctx();
  ctx.resume().catch(() => {});

  setup(g).catch((err) => {
    if (g !== gen) return;
    failTeacher(err?.name === 'NotAllowedError' ? '麦克风权限被拒绝，请在浏览器设置里允许后重试' : `启动失败：${err.message}`);
  });
}

async function setup(g) {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { echoCancellation: true, noiseSuppression: true },
  });
  if (g !== gen) {
    stream.getTracks().forEach((t) => t.stop());
    return;
  }
  micStream = stream;

  await ctx.audioWorklet.addModule(workletUrl());
  if (g !== gen) return;
  micSrcNode = ctx.createMediaStreamSource(stream);
  micNode = new AudioWorkletNode(ctx, 'phonics-mic');
  micNode.port.onmessage = (e) => {
    if (g === gen) sendMicFrame(e.data);
  };
  // worklet 不连进音频图不会被驱动；经 0 增益接 destination（听不到自己）
  micMuteGain = ctx.createGain();
  micMuteGain.gain.value = 0;
  micSrcNode.connect(micNode);
  micNode.connect(micMuteGain);
  micMuteGain.connect(ctx.destination);

  // BASE 支持子路径反代部署（api.js 顶部注释），WS 地址与 REST 同前缀
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}${BASE}api/teacher/live`);
  ws.binaryType = 'arraybuffer';
  ws.onmessage = (e) => {
    if (g === gen) onWsMessage(e);
  };
  ws.onclose = () => {
    if (g !== gen) return;
    // 用户主动 stop 时代数已递增，走不到这里；能到这里都是异常断开
    failTeacher(teacherState.error || 'AI 老师连接已断开');
  };
  ws.onerror = () => {
    // 统一由 onclose 收尾
  };
}

/** 关闭 AI 老师（用户主动）。 */
export function stopTeacher() {
  gen++;
  teardown();
  teacherState.status = 'idle';
  teacherState.error = '';
}

function failTeacher(message) {
  gen++;
  teardown();
  teacherState.status = 'error';
  teacherState.error = message;
  toastError(message);
}

function teardown() {
  clearTimeout(cardDebounce);
  cardDebounce = 0;
  stopDownstream();
  if (ws) {
    ws.onmessage = ws.onclose = ws.onerror = null;
    try {
      ws.close();
    } catch {
      // 已关闭
    }
    ws = null;
  }
  if (micNode) {
    micNode.port.onmessage = null;
    micNode = null;
  }
  micSrcNode = null;
  micMuteGain = null;
  if (micStream) {
    micStream.getTracks().forEach((t) => t.stop());
    micStream = null;
  }
  if (ctx) {
    ctx.close().catch(() => {});
    ctx = null;
  }
  micPausedForPlayback = false;
}

// —— 上行 ——

function sendMicFrame(buf) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  // 本地在播卡片音频：暂停上行（老师不该听见卡片音频），通知一次即可
  if (playback.url || playback.segment) {
    if (!micPausedForPlayback) {
      micPausedForPlayback = true;
      sendJSON({ type: 'mic', on: false });
    }
    return;
  }
  micPausedForPlayback = false;
  ws.send(buf);
}

function sendJSON(v) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(v));
}

/** 当前单词组变化时调用（按组 id 去重，轮询换引用不会重发）。 */
export function syncGroup(group) {
  if (!connected() || !group?.id) return;
  if (group.id === lastSentGroupId) return;
  lastSentGroupId = group.id;
  sendJSON({
    type: 'context',
    group: { name: group.name || '', words: (group.words ?? []).map((w) => w.word) },
  });
}

/** 当前单词卡变化时调用（800ms 防抖 + slug 去重，连续翻卡只注入停留卡）。 */
export function syncCard(word, card) {
  if (!connected() || !word) return;
  clearTimeout(cardDebounce);
  const g = gen;
  cardDebounce = setTimeout(() => {
    if (g !== gen || !connected()) return;
    const slug = word.toLowerCase();
    if (slug === lastSentSlug) return;
    lastSentSlug = slug;
    sendJSON({
      type: 'card',
      word,
      ipa: card?.ipa || '',
      zh: card?.senses?.[0]?.zh || '',
    });
  }, CARD_DEBOUNCE_MS);
}

function connected() {
  return ws && ws.readyState === WebSocket.OPEN && teacherState.status !== 'connecting';
}

// 收到 ready 后主动补一次当前上下文，覆盖"先进组再开老师"的方向；
// "先开老师再进组"由 TeacherFab 的 $effect 覆盖。
function syncNow() {
  const group = playerState.group;
  if (!group) return;
  syncGroup(group);
  const words = group.words ?? [];
  if (!words.length) return;
  const w = words[playerState.index % words.length];
  if (w) syncCard(w.word, playerState.cards[w.slug]);
}

// —— 下行 ——

function onWsMessage(e) {
  if (e.data instanceof ArrayBuffer) {
    playChunk(e.data);
    return;
  }
  let m;
  try {
    m = JSON.parse(e.data);
  } catch {
    return;
  }
  switch (m.type) {
    case 'ready':
      teacherState.status = 'listening';
      syncNow();
      break;
    case 'transcript':
      addTranscript(m.role, m.text);
      break;
    case 'interrupted':
      stopDownstream();
      finalizeTranscript();
      if (teacherState.status === 'speaking') teacherState.status = 'listening';
      break;
    case 'turn_complete':
      // 音频可能还在排队播放（服务端下发快于实时），状态由播放队列排空驱动
      finalizeTranscript();
      break;
    case 'restarted':
      // 上游会话轮换重连成功，上下文已由后端重放，无感继续
      break;
    case 'error':
      if (m.fatal) {
        teacherState.error = m.message || 'AI 老师出错了';
      } else {
        toastError(m.message || 'AI 老师出错了');
      }
      break;
  }
}

function playChunk(buf) {
  if (!ctx) return;
  const i16 = new Int16Array(buf);
  if (i16.length === 0) return;
  const f32 = new Float32Array(i16.length);
  for (let i = 0; i < i16.length; i++) f32[i] = i16[i] / 0x8000;
  const audioBuf = ctx.createBuffer(1, f32.length, DOWN_SAMPLE_RATE);
  audioBuf.getChannelData(0).set(f32);
  const src = ctx.createBufferSource();
  src.buffer = audioBuf;
  src.connect(ctx.destination);
  const g = gen;
  const startAt = Math.max(ctx.currentTime + 0.08, nextT);
  src.onended = () => {
    if (g !== gen) return;
    activeSources.delete(src);
    if (activeSources.size === 0 && teacherState.status === 'speaking') {
      teacherState.status = 'listening';
    }
  };
  activeSources.add(src);
  src.start(startAt);
  nextT = startAt + audioBuf.duration;
  if (teacherState.status === 'listening') teacherState.status = 'speaking';
}

// 学生打断（barge-in）：立即清空播放队列闭嘴
function stopDownstream() {
  for (const src of activeSources) {
    src.onended = null;
    try {
      src.stop();
    } catch {
      // 未 start 或已结束
    }
  }
  activeSources.clear();
  nextT = 0;
}

// —— 字幕 ——

// Live 的转写按增量片段到达：同角色未定稿条目就地追加，轮次边界定稿。
function addTranscript(role, text) {
  if (!text) return;
  const arr = teacherState.transcript;
  const last = arr[arr.length - 1];
  if (last && last.role === role && !last.final) {
    last.text += text;
  } else {
    arr.push({ role, text, final: false });
    if (arr.length > TRANSCRIPT_LIMIT) arr.splice(0, arr.length - TRANSCRIPT_LIMIT);
  }
}

function finalizeTranscript() {
  for (const t of teacherState.transcript) t.final = true;
}

// —— 页面可见性：iOS 切后台会挂起 WS，回前台时死连接置错误态 ——

if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible') return;
    if (teacherState.status === 'idle' || teacherState.status === 'error') return;
    if (!ws || ws.readyState === WebSocket.CLOSING || ws.readyState === WebSocket.CLOSED) {
      failTeacher('连接已断开，请重新开启 AI 老师');
    }
  });
}
