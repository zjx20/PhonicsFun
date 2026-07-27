// 首页组列表状态 + 轮询控制。
// 存在未全就绪（ready < total）的组时每 5 秒轮询一次 GET /api/groups，全就绪即停。

import * as api from './api.js';
import { toastError, toastSuccess } from './toast.svelte.js';

export const groupsState = $state({
  groups: [],
  loading: false,
  loaded: false,
  error: '',
});

let pollTimer = null;
let active = false;
let pollErrorShown = false;

export function startGroupsPolling() {
  active = true;
  refreshGroups();
}

export function stopGroupsPolling() {
  active = false;
  clearTimeout(pollTimer);
  pollTimer = null;
}

export async function refreshGroups({ silent = false } = {}) {
  if (!silent) groupsState.loading = true;
  try {
    const list = await api.listGroups();
    groupsState.groups = Array.isArray(list) ? list : [];
    groupsState.loaded = true;
    groupsState.error = '';
    pollErrorShown = false;
  } catch (err) {
    groupsState.error = err.message;
    // 首次加载失败由页面展示错误块；轮询失败只弹一次 toast，避免每 5 秒刷屏
    if (silent && !pollErrorShown) {
      toastError(err.message);
      pollErrorShown = true;
    }
  } finally {
    groupsState.loading = false;
  }
  schedule();
}

function schedule() {
  if (!active) return;
  clearTimeout(pollTimer);
  const pending = groupsState.groups.some((g) => (g.ready ?? 0) < (g.total ?? 0));
  const retrying = groupsState.loaded && groupsState.error;
  if (pending || retrying) {
    pollTimer = setTimeout(() => refreshGroups({ silent: true }), 5000);
  }
}

export async function removeGroup(id) {
  try {
    await api.deleteGroup(id);
    groupsState.groups = groupsState.groups.filter((g) => g.id !== id);
    toastSuccess('已删除单词组');
  } catch (err) {
    toastError(err.message);
  }
}
