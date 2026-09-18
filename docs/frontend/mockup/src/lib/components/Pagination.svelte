<script lang="ts">
  interface Props {
    /** 1-based. */
    page: number;
    pageCount: number;
    /** Total rows across all pages, shown between the controls. */
    total?: number;
    onchange: (page: number) => void;
    label?: string;
  }

  let { page, pageCount, total = undefined, onchange, label = "Pagination" }: Props = $props();
</script>

<nav class="pager" aria-label={label}>
  <button
    type="button"
    disabled={page <= 1}
    onclick={() => onchange(page - 1)}
  >← Prev</button>

  <p class="status num" aria-live="polite">
    <span class="sr">Page</span>{page}<span class="sr"> of </span> / {pageCount}
    {#if total !== undefined}
      <span class="total">· {total.toLocaleString("en-GB")} rows</span>
    {/if}
  </p>

  <button
    type="button"
    disabled={page >= pageCount}
    onclick={() => onchange(page + 1)}
  >Next →</button>
</nav>

<style>
  .pager {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  button {
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    background: var(--c-surface);
    border: 0;
    box-shadow: var(--ring);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    cursor: pointer;
  }

  button:hover:not(:disabled) {
    box-shadow: var(--ring), 4px 4px 0 var(--c-blue);
  }

  button:disabled {
    color: var(--c-text-disabled);
    box-shadow: 0 0 0 2px var(--c-rule);
    cursor: not-allowed;
  }

  .status {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }

  .total {
    color: var(--c-text-muted);
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
