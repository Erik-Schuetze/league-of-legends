<script lang="ts">
  import SampleAnnotation from "./SampleAnnotation.svelte";
  import DeltaIndicator from "./DeltaIndicator.svelte";

  interface Props {
    label: string;
    /** Pre-formatted value. Numbers are formatted where they are computed so
     *  the component never guesses at precision. */
    value: string;
    unit?: string;
    /** One sentence of context under the value. Optional but almost always
     *  worth writing: a KPI with no scope is a vanity number. */
    note?: string;
    /** Sample size. Omit only for figures that are not samples (counts of
     *  published cells, for instance). */
    n?: number;
    minCellN?: number;
    /** Renders the withheld treatment instead of a value. */
    withheld?: boolean;
    delta?: { value: number; unit?: string; baseline: string; inverted?: boolean; decimals?: number };
    /** Spoken state for loading, which has no value yet. */
    loading?: boolean;
    href?: string;
  }

  let {
    label,
    value,
    unit = undefined,
    note = undefined,
    n = undefined,
    minCellN = undefined,
    withheld = false,
    delta = undefined,
    loading = false,
    href = undefined,
  }: Props = $props();

  const Tag = $derived(href ? "a" : "div");
</script>

<svelte:element this={Tag} class="stat" data-loading={loading} data-withheld={withheld} href={href}>
  <span class="label">{label}</span>
  {#if loading}
    <span class="value placeholder" aria-hidden="true">— — —</span>
    <span class="visually-hidden">Loading {label}</span>
  {:else if withheld}
    <span class="value muted">withheld</span>
  {:else}
    <span class="value num"
      >{value}{#if unit}<span class="unit">{unit}</span>{/if}</span
    >
  {/if}
  {#if delta && !loading && !withheld}
    <DeltaIndicator {...delta} size="sm" />
  {/if}
  {#if note}
    <span class="note">{note}</span>
  {/if}
  {#if n !== undefined}
    <SampleAnnotation {n} {minCellN} {withheld} size="sm" />
  {/if}
</svelte:element>

<style>
  .stat {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    text-decoration: none;
    color: inherit;
    align-content: start;
  }

  a.stat:hover {
    transform: var(--lift);
    box-shadow: 0 0 0 var(--ring-width) var(--c-blue), var(--offset-hover);
  }

  .label {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .value {
    font-family: var(--font-heading);
    font-size: var(--step-4);
    font-weight: var(--fw-bold);
    font-feature-settings: var(--num-features);
    line-height: var(--lh-tight);
    color: var(--c-ink);
  }

  .value.muted {
    font-size: var(--step-1);
    color: var(--sig-unknown);
    text-transform: none;
  }

  .unit {
    font-size: var(--step-0);
    color: var(--c-text-muted);
    margin-inline-start: 0.15em;
  }

  .value.placeholder {
    color: var(--c-text-disabled);
    letter-spacing: 0.1em;
  }

  .note {
    font-size: var(--step--1);
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .visually-hidden {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  @media (prefers-reduced-motion: reduce) {
    a.stat:hover {
      transform: none;
    }
  }
</style>
