<script lang="ts">
  interface Props {
    /** The cell's sample size. Mandatory on every published statistic. */
    n: number;
    /** The build's suppression floor, from the envelope. Shown in the
     *  explanation so the reader knows why a neighbour may be missing. */
    minCellN?: number;
    /** Renders the withheld treatment instead of a count. */
    withheld?: boolean;
    /** Adds the population the sample came from, e.g. "of 8,905 cells". */
    of?: number;
    size?: "sm" | "md";
  }

  let {
    n,
    minCellN = undefined,
    withheld = false,
    of: ofTotal = undefined,
    size = "md",
  }: Props = $props();

  const formatted = $derived(n.toLocaleString("en-GB"));

  const explanation = $derived(
    withheld
      ? `Sample size withheld: fewer than ${minCellN ?? "the minimum"} games in this cell.`
      : `${formatted} games behind this figure.` +
        (minCellN ? ` Cells below ${minCellN.toLocaleString("en-GB")} games are withheld.` : ""),
  );
</script>

<span class="n" data-size={size} data-withheld={withheld} title={explanation}>
  <span class="visually-hidden">{explanation}</span>
  <span aria-hidden="true">
    {#if withheld}
      n withheld{#if minCellN}<span class="floor"> (&lt;{minCellN.toLocaleString("en-GB")})</span>{/if}
    {:else}
      n {formatted}{#if ofTotal}<span class="floor"> / {ofTotal.toLocaleString("en-GB")}</span>{/if}
    {/if}
  </span>
</span>

<style>
  .n {
    font-family: var(--font-mono);
    font-feature-settings: var(--num-features);
    font-size: var(--step--2);
    color: var(--c-text-muted);
    white-space: nowrap;
  }

  .n[data-size="sm"] {
    font-size: 0.6875rem;
  }

  .floor {
    color: var(--c-text-muted);
  }

  /* Withheld is a distinct state, and it is grey, not red: nothing failed,
     the value is simply not publishable. */
  .n[data-withheld="true"] {
    color: var(--sig-unknown);
    letter-spacing: 0.02em;
  }

  .visually-hidden {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
