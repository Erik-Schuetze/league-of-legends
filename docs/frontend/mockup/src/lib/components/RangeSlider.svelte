<script lang="ts">
  interface Props {
    label: string;
    value: number;
    min?: number;
    max?: number;
    step?: number;
    /** Formats the readout, e.g. win-rate as a percentage. */
    format?: (value: number) => string;
    onchange?: (value: number) => void;
    hint?: string;
  }

  let {
    label,
    value = $bindable(),
    min = 0,
    max = 100,
    step = 1,
    format = (v: number) => String(v),
    onchange,
    hint = undefined,
  }: Props = $props();

  const percent = $derived(((value - min) / (max - min)) * 100);
</script>

<div class="range">
  <div class="head">
    <span class="label" id={`${label}-range-label`}>{label}</span>
    <output class="readout num" for={`${label}-range`}>{format(value)}</output>
  </div>
  <input
    id={`${label}-range`}
    type="range"
    {min}
    {max}
    {step}
    bind:value
    aria-describedby={`${label}-range-label`}
    onchange={() => onchange?.(value)}
    style="--fill: {percent}%"
  />
  {#if hint}
    <p class="hint">{hint}</p>
  {/if}
</div>

<style>
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-3);
    margin-block-end: var(--space-2);
  }

  .label {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .readout {
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
  }

  /* Square track, square thumb, a hard blue fill for the travelled side. */
  input[type="range"] {
    appearance: none;
    inline-size: 100%;
    block-size: var(--touch-target);
    background: none;
    cursor: pointer;
  }

  input[type="range"]::-webkit-slider-runnable-track {
    block-size: 0.5rem;
    background:
      linear-gradient(var(--c-blue), var(--c-blue)) 0 / var(--fill, 0%) 100% no-repeat,
      var(--c-surface);
    box-shadow: 0 0 0 2px var(--c-control-border);
  }

  input[type="range"]::-moz-range-track {
    block-size: 0.5rem;
    background: var(--c-surface);
    box-shadow: 0 0 0 2px var(--c-control-border);
  }

  input[type="range"]::-moz-range-progress {
    block-size: 0.5rem;
    background: var(--c-blue);
  }

  input[type="range"]::-webkit-slider-thumb {
    appearance: none;
    inline-size: 1.25rem;
    block-size: 1.25rem;
    margin-block-start: -0.4rem;
    background: var(--c-cream);
    box-shadow: 0 0 0 2px var(--c-blue);
  }

  input[type="range"]::-moz-range-thumb {
    inline-size: 1.25rem;
    block-size: 1.25rem;
    border: 0;
    border-radius: 0;
    background: var(--c-cream);
    box-shadow: 0 0 0 2px var(--c-blue);
  }

  input[type="range"]:focus-visible::-webkit-slider-thumb {
    box-shadow: var(--focus-shadow);
  }

  .hint {
    margin-block-start: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }
</style>
