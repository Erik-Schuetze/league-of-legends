<script lang="ts">
  interface Props {
    /** What was filtered, in the visitor's words. */
    what?: string;
    /** Which filters are active, so the reader can see why nothing matched. */
    activeFilters?: { label: string; value: string }[];
    /** Resets every filter. Always offered: an empty result must be escapable. */
    onclear?: () => void;
    /** The unfiltered size, which proves data exists and the filters hid it. */
    availableCount?: { rows: number; noun: string };
    variant?: "table" | "page";
  }

  let {
    what = "cells",
    activeFilters = [],
    onclear = undefined,
    availableCount = undefined,
    variant = "table",
  }: Props = $props();

  const fmt = (n: number) => n.toLocaleString("en-GB");
</script>

<div class="empty" data-variant={variant} role="status">
  <p class="stamp">No matches</p>
  <h3 class="heading">No {what} match these filters</h3>
  <p class="body">
    The filters are valid; they simply select nothing in this slice.
    {#if availableCount}
      {fmt(availableCount.rows)} {availableCount.noun} are published for the current patch, region,
      queue and bracket.
    {/if}
  </p>
  {#if activeFilters.length > 0}
    <ul class="filters">
      {#each activeFilters as filter (filter.label + filter.value)}
        <li>
          <span class="fl">{filter.label}</span>
          <span class="fv num">{filter.value}</span>
        </li>
      {/each}
    </ul>
  {/if}
  {#if onclear}
    <button type="button" class="clear" onclick={onclear}>Clear all filters</button>
  {/if}
</div>

<style>
  .empty {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    padding: var(--space-5);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
  }

  .empty[data-variant="page"] {
    max-inline-size: var(--measure);
  }

  .stamp {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .heading {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-1);
    color: var(--c-text-strong);
  }

  .body {
    margin: 0;
    color: var(--c-text);
    line-height: var(--lh-body);
    max-inline-size: 60ch;
  }

  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .filters li {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    font-size: var(--step--2);
  }

  .fl {
    color: var(--c-text-muted);
    text-transform: uppercase;
    letter-spacing: var(--ls-wide);
    font-family: var(--font-mono);
  }

  .fv {
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
  }

  .clear {
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--ring);
    border: 0;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-accent-strong);
    cursor: pointer;
  }

  .clear:hover {
    background: var(--c-blue);
    color: var(--c-cream);
  }
</style>
