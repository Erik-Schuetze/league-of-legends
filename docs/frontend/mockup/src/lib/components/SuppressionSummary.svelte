<script lang="ts">
  interface Props {
    /** From the envelope: how many cells the build withheld. */
    suppressedCells: number;
    /** From the envelope: the games floor a cell must clear. */
    minCellN: number;
    /** From the envelope: how many cells were published. */
    publishedCells?: number;
    /** Pairs that exist in the champion catalogue but have no published cell. */
    unpublishedPairs?: number;
    /** Compact fits in a filter bar; full gets its own card. */
    density?: "compact" | "full";
  }

  let {
    suppressedCells,
    minCellN,
    publishedCells = undefined,
    unpublishedPairs = undefined,
    density = "full",
  }: Props = $props();

  /* The heading id must be unique: the page can legitimately show the same
     summary in a filter bar and in a card below the table. */
  const uid = $props.id();
  const headingId = `suppression-heading-${uid}`;

  const fmt = (v: number) => v.toLocaleString("en-GB");
</script>

<section class="suppression" data-density={density} aria-labelledby={headingId}>
  <h3 id={headingId} class="heading">
    {fmt(suppressedCells)} {suppressedCells === 1 ? "cell was" : "cells were"} withheld from this build
  </h3>
  <p class="body">
    A cell needs at least <strong class="num">{fmt(minCellN)}</strong> games before it is published.
    Below that floor a win rate swings by several points from noise alone, so it is left out rather
    than shown with a caveat.
    {#if publishedCells !== undefined}
      <strong class="num">{fmt(publishedCells)}</strong> cells met the floor.
    {/if}
    {#if unpublishedPairs !== undefined}
      <strong class="num">{fmt(unpublishedPairs)}</strong> champion-and-role pairs have no published
      cell at all in this slice.
    {/if}
  </p>
  <p class="body small">
    Withheld cells are shown as withheld, never as zero and never as a gap that could be mistaken for
    a rendering problem.
  </p>
</section>

<style>
  .suppression {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
    border-inline-start: 6px solid var(--sig-unknown);
  }

  .suppression[data-density="compact"] {
    padding: var(--space-3);
    gap: var(--space-1);
  }

  .heading {
    font-family: var(--font-heading);
    font-size: var(--step-0);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
    margin: 0;
  }

  .body {
    margin: 0;
    max-inline-size: 68ch;
    font-size: var(--step--1);
    line-height: var(--lh-body);
    color: var(--c-text);
  }

  .body.small {
    color: var(--c-text-muted);
  }

  strong.num {
    font-family: var(--font-mono);
    font-feature-settings: var(--num-features);
    color: var(--c-text-strong);
  }
</style>
