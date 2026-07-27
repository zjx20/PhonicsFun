// 全局 Toast 状态：API 错误红色、成功操作绿色，3 秒自动消失。

export const toasts = $state([]);

let nextId = 1;

export function showToast(message, type = 'error') {
  const id = nextId++;
  toasts.push({ id, message, type });
  setTimeout(() => {
    const index = toasts.findIndex((t) => t.id === id);
    if (index !== -1) toasts.splice(index, 1);
  }, 3000);
}

export function toastError(message) {
  showToast(message, 'error');
}

export function toastSuccess(message) {
  showToast(message, 'success');
}
