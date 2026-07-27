<script>
  let { word, removed = false, ontoggle } = $props();
</script>

<button class="word-chip" class:removed onclick={ontoggle} aria-pressed={removed}>
  {word}
</button>

<style>
  .word-chip {
    min-height: 48px;
    padding: 0 18px;
    border: 2px solid var(--primary-soft-border);
    border-radius: 999px;
    background: var(--card);
    font-size: 18px;
    font-weight: 700;
    color: var(--text);
    cursor: pointer;
    transition:
      opacity 0.15s ease,
      transform 0.1s ease;
  }
  .word-chip:active {
    transform: scale(0.95);
  }
  .word-chip.removed {
    text-decoration: line-through;
    text-decoration-thickness: 2px;
    color: var(--muted);
    border-color: var(--track);
    background: var(--track);
    opacity: 0.65;
  }
</style>
