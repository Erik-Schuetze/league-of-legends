<script lang="ts">
  interface Props {
    /** What is being loaded, in words, for screen readers and for the layout. */
    label: string;
    /** "rows" mimics a table body, "card" a tile, "text" a paragraph. */
    shape?: "rows" | "card" | "text";
    rows?: number;
  }

  let { label, shape = "rows", rows = 6 }: Props = $props();
</script>

<div class="skeleton" data-shape={shape} role="status" aria-live="polite">
  <span class="spoken">{label}… loading</span>
  {#if shape === "rows"}
    <div class="bars" aria-hidden="true">
      {#each Array.from({ length: rows }, (_, i) => i) as i (i)}
        <span class="bar" style={`--w: ${58 + ((i * 37) % 38)}%`}></span>
      {/each}
    </div>
  {:else if shape === "card"}
    <span class="block" aria-hidden="true"></span>
  {:else}
    <div class="bars" aria-hidden="true">
      <span class="bar" style="--w: 92%"></span>
      <span class="bar" style="--w: 76%"></span>
      <span class="bar" style="--w: 84%"></span>
    </div>
  {/if}
</div>

<style>
  .skeleton {
    display: grid;
    gap: var(--space-2);
  }

  .skeleton[data-shape="card"] {
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
  }

  .bars {
    display: grid;
    gap: var(--space-2);
  }

  /* Skeleton bars are paper, not shimmer: the same flat language as the rest
     of the page, with a slow opacity pulse as the only motion. */
  .bar {
    display: block;
    block-size: 0.75rem;
    inline-size: var(--w, 60%);
    background: var(--tint-accent);
    box-shadow: inset 0 0 0 2px var(--c-rule);
    animation: pulse 1.6s var(--ease) infinite;
  }

  .block {
    display: block;
    block-size: 5rem;
    background: var(--tint-accent);
    box-shadow: inset 0 0 0 2px var(--c-rule);
    animation: pulse 1.6s var(--ease) infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.5; }
  }

  @media (prefers-reduced-motion: reduce) {
    .bar,
    .block {
      animation: none;
    }
  }

  .spoken {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
