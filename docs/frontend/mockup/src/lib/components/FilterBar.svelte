<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Controls, laid out in a wrapping row. */
    children: Snippet;
    /** Active filter chips and the "clear all" action. */
    chips?: Snippet;
    /** Right-hand summary: how many rows matched, and the coverage line. */
    summary?: Snippet;
    /** Sticky keeps the filters reachable while scrolling a long table. */
    sticky?: boolean;
  }

  let { children, chips, summary, sticky = false }: Props = $props();
</script>

<section class="filter-bar" class:sticky aria-label="Filters">
  <div class="controls">{@render children()}</div>
  {#if summary}
    <div class="summary">{@render summary()}</div>
  {/if}
  {#if chips}
    <div class="chips">{@render chips()}</div>
  {/if}
</section>

<style>
  .filter-bar {
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    padding: var(--space-4);
    display: grid;
    gap: var(--space-3);
  }

  .filter-bar.sticky {
    position: sticky;
    inset-block-start: calc(var(--nav-height) + var(--space-2));
    z-index: 10;
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3) var(--space-4);
    align-items: end;
  }

  .summary {
    font-size: var(--step--2);
    color: var(--c-text-muted);
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    align-items: baseline;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
    padding-block-start: var(--space-1);
  }
</style>
