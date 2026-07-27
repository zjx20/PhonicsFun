<script>
  import { fly } from 'svelte/transition';
  import { toasts } from '../lib/toast.svelte.js';
</script>

<div class="toast-stack" aria-live="polite">
  {#each toasts as toast (toast.id)}
    <div class="toast toast-{toast.type}" transition:fly={{ y: -24, duration: 180 }}>
      {toast.type === 'success' ? '✅' : '❌'}
      {toast.message}
    </div>
  {/each}
</div>

<style>
  .toast-stack {
    position: fixed;
    top: max(12px, env(safe-area-inset-top));
    left: 50%;
    transform: translateX(-50%);
    z-index: 1000;
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: min(92vw, 480px);
    pointer-events: none;
  }
  .toast {
    padding: 12px 16px;
    border-radius: 14px;
    color: #fff;
    font-size: 15px;
    font-weight: 600;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18);
    word-break: break-word;
  }
  .toast-error {
    background: var(--red);
  }
  .toast-success {
    background: var(--green);
  }
</style>
