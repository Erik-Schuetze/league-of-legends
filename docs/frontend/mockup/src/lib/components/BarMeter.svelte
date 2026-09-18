<script lang="ts">
  import SampleAnnotation from "./SampleAnnotation.svelte";

  interface Props {
    /** 0-1. Rates are already computed upstream; this component only draws. */
    value: number | undefined;
    label: string;
    /** Word form of the value, e.g. "52.4%". Used as the visible text so the
     *  bar is never the only encoding. */
    text: string;
    /** Reference line, 0-1, e.g. the 50% coin flip or the slice median. */
    reference?: { at: number; label: string };
    /** Renders the ordered encoding: low is good or high is good. */
    polarity?: "neutral" | "high-good" | "low-good";
    n?: number;
    minCellN?: number;
    withheld?: boolean;
    size?: "sm" | "md";
  }

  let {
    value,
    label,
    text,
    reference = undefined,
    polarity = "neutral",
    n = undefined,
    minCellN = undefined,
    withheld = false,
    size = "md",
  }: Props = $props();

  const pct = $derived(value === undefined ? 0 : Math.max(0, Math.min(1, value)) * 100);
  const tone = $derived(
    value === undefined || withheld || polarity === "neutral"
      ? "flat"
      : polarity === "high-good"
        ? value >= (reference?.at ?? 0.5)
          ? "up"
          : "down"
        : value <= (reference?.at ?? 0.5)
          ? "up"
          : "down",
  );
</script>

<div class="meter" data-size={size} data-tone={tone} data-withheld={withheld}>
  <div class="head">
    <span class="label">{label}</span>
    <span class="text num">{withheld ? "withheld" : text}</span>
  </div>
  <div class="track" role="img" aria-label={`${label}: ${withheld ? "withheld" : text}`}>
    {#if !withheld && value !== undefined}
      <span class="fill" style={`inline-size: ${pct}%`}></span>
    {/if}
    {#if reference}
      <span class="ref" style={`inset-inline-start: ${reference.at * 100}%`}></span>
    {/if}
    {#if withheld}
      <span class="hatch" aria-hidden="true"></span>
    {/if}
  </div>
  <div class="foot">
    {#if reference}
      <span class="ref-label">{reference.label}</span>
    {/if}
    {#if n !== undefined}
      <SampleAnnotation {n} {minCellN} {withheld} size="sm" />
    {/if}
  </div>
</div>

<style>
  .meter {
    display: grid;
    gap: var(--space-2);
    min-inline-size: 9rem;
  }

  .head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-3);
  }

  .label {
    font-size: var(--step--1);
    color: var(--c-text-strong);
  }

  .text {
    font-weight: var(--fw-bold);
    font-size: var(--step--1);
  }

  .meter[data-size="sm"] .label,
  .meter[data-size="sm"] .text {
    font-size: var(--step--2);
  }

  /* The track is an inset well, not a rounded pill: nothing in this design
     rounds a corner. */
  .track {
    position: relative;
    block-size: var(--bar);
    background: var(--c-sand);
    box-shadow: var(--shadow-inset);
    overflow: hidden;
  }

  .meter[data-size="sm"] { --bar: 0.5rem; }
  .meter[data-size="md"] { --bar: 0.75rem; }

  .fill {
    position: absolute;
    inset-block: 0;
    inset-inline-start: 0;
    background: var(--c-blue);
  }

  .meter[data-tone="up"] .fill {
    background: var(--sig-up);
  }

  .meter[data-tone="down"] .fill {
    background: var(--sig-down);
  }

  .ref {
    position: absolute;
    inset-block: 0;
    inline-size: 2px;
    background: var(--c-ink);
  }

  /* Withheld is drawn as a diagonal hatch on paper, never as a zero-length
     bar: an empty bar would read as "zero". */
  .hatch {
    position: absolute;
    inset: 0;
    background: repeating-linear-gradient(
      -45deg,
      transparent 0 4px,
      var(--c-rule) 4px 6px
    );
  }

  .foot {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-3);
  }

  .ref-label {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }
</style>
