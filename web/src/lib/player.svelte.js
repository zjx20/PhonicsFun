// 播放页状态：当前组、卡片缓存、索引，以及两类轮询——
// 1) 组内有词未就绪（text/audio 为 pending/running）时每 2 秒轮询组状态，全就绪即停；
// 2) 重新生成任务：提交后同样靠该轮询跟踪，直到 done/failed。
//    注意 target=text 时后端会级联重生音频，所以 text/both 都要等 text 和 audio 全部 done。

import * as api from './api.js';
import { toastError, toastSuccess } from './toast.svelte.js';

export const playerState = $state({
  groupId: null,
  group: null, // GET /api/groups/{id} 的返回
  cards: {}, // slug -> card.json
  index: 0,
  dir: 1, // 翻页方向，供进场动画使用：1 向左翻（下一个），-1 向右翻
  loading: false,
  error: '',
  regenerating: {}, // slug -> { target, startedAt, seenActive }
});

let pollTimer = null;
let active = false;
let pollErrorShown = false;
const cardFetches = new Set();
const cardFailures = new Map(); // slug -> 连续失败次数，>=3 后不再自动重试（force 可重置）

const inFlight = (s) => s === 'pending' || s === 'running';

export async function openGroup(id) {
  closeGroup();
  active = true;
  playerState.groupId = id;
  playerState.group = null;
  playerState.cards = {};
  playerState.index = 0;
  playerState.dir = 1;
  playerState.error = '';
  playerState.regenerating = {};
  playerState.loading = true;
  cardFailures.clear();
  pollErrorShown = false;
  await refreshGroup({ initial: true });
  playerState.loading = false;
}

export function closeGroup() {
  active = false;
  clearTimeout(pollTimer);
  pollTimer = null;
}

async function refreshGroup({ initial = false } = {}) {
  const id = playerState.groupId;
  try {
    const detail = await api.getGroup(id);
    if (!active || playerState.groupId !== id) return;
    playerState.group = detail;
    playerState.error = '';
    pollErrorShown = false;
    const words = detail.words ?? [];
    if (playerState.index >= words.length) playerState.index = 0;
    settleRegenerations(words);
    for (const w of words) {
      if (w.text === 'done' && !playerState.cards[w.slug] && !playerState.regenerating[w.slug]) {
        fetchCard(w.slug);
      }
    }
  } catch (err) {
    if (!active || playerState.groupId !== id) return;
    if (initial) {
      playerState.error = err.message;
    } else if (!pollErrorShown) {
      toastError(err.message);
      pollErrorShown = true;
    }
  }
  schedulePoll();
}

function needsPolling() {
  const g = playerState.group;
  if (!g) return false;
  if (Object.keys(playerState.regenerating).length > 0) return true;
  return (g.words ?? []).some((w) => inFlight(w.text) || inFlight(w.audio));
}

function schedulePoll() {
  if (!active) return;
  clearTimeout(pollTimer);
  if (needsPolling()) {
    pollTimer = setTimeout(() => refreshGroup(), 2000);
  }
}

// 检查在途的重新生成任务是否已到达终态。
// 刚提交后后端可能还没把状态翻成 pending，所以要么观察到过 pending/running（seenActive），
// 要么超过 4 秒宽限期，才认可 done/failed，避免把旧状态误判为“已完成”。
function settleRegenerations(words) {
  for (const [slug, job] of Object.entries(playerState.regenerating)) {
    const w = words.find((x) => x.slug === slug);
    if (!w) {
      delete playerState.regenerating[slug];
      continue;
    }
    const relevant = job.target === 'audio' ? [w.audio] : [w.text, w.audio];
    if (relevant.some(inFlight)) {
      job.seenActive = true;
      continue;
    }
    const inGrace = !job.seenActive && Date.now() - job.startedAt < 4000;
    if (relevant.every((s) => s === 'done')) {
      if (inGrace) continue;
      delete playerState.regenerating[slug];
      fetchCard(slug, { force: true });
      toastSuccess(`「${w.word}」重新生成完成`);
    } else if (relevant.includes('failed')) {
      if (inGrace) continue;
      delete playerState.regenerating[slug];
      toastError(w.error ? `「${w.word}」重新生成失败：${w.error}` : `「${w.word}」重新生成失败`);
    }
  }
}

async function fetchCard(slug, { force = false } = {}) {
  if (cardFetches.has(slug)) return;
  if (force) cardFailures.delete(slug);
  else if ((cardFailures.get(slug) ?? 0) >= 3) return;
  cardFetches.add(slug);
  try {
    const card = await api.getCard(slug);
    if (!active) return;
    playerState.cards[slug] = card;
    cardFailures.delete(slug);
  } catch (err) {
    const count = (cardFailures.get(slug) ?? 0) + 1;
    cardFailures.set(slug, count);
    if (count === 1) toastError(`加载「${slug}」卡片失败：${err.message}`);
  } finally {
    cardFetches.delete(slug);
  }
}

export async function regenerate(slug, target) {
  try {
    await api.regenerateWord(slug, target);
    playerState.regenerating[slug] = { target, startedAt: Date.now(), seenActive: false };
    toastSuccess('已提交重新生成');
    // 尽快开始跟踪状态
    clearTimeout(pollTimer);
    pollTimer = setTimeout(() => refreshGroup(), 1000);
  } catch (err) {
    toastError(err.message);
  }
}

export function nextCard() {
  const len = playerState.group?.words?.length ?? 0;
  if (len === 0) return;
  playerState.dir = 1;
  playerState.index = (playerState.index + 1) % len;
}

export function prevCard() {
  const len = playerState.group?.words?.length ?? 0;
  if (len === 0) return;
  playerState.dir = -1;
  playerState.index = (playerState.index - 1 + len) % len;
}
