<script lang="ts">
  interface Option {
    value: string;
    label: string;
    hint?: string;
  }

  interface Props {
    options: Option[];
    value: string;
    onchange?: (value: string) => void;
    /** Group name, rendered as the legend when `label` is given. */
    name: string;
    label?: string;
    /** Stack vertically. Use for more than four options. */
    stacked?: boolean;
  }

  let {
    options,
    value = $bindable(),
    onchange,
    name,
    label = undefined,
    stacked = false,
  }: Props = $props();
</script>

<fieldset class="group" data-stacked={stacked}>
  {#if label}
    <legend>{label}</legend>
  {/if}
  {#each options as option (option.value)}
    <label class="radio">
      <input
        type="radio"
        {name}
        value={option.value}
        checked={value === option.value}
        onchange={() => {
          value = option.value;
          onchange?.(option.value);
        }}
      />
      <span class="mark" aria-hidden="true"></span>
      <span class="text">
        <span class="label">{option.label}</span>
        {#if option.hint}
          <span class="hint">{option.hint}</span>
        {/if}
      </span>
    </label>
  {/each}
</fieldset>

<style>
  .group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-5);
    margin: 0;
    padding: 0;
    border: 0;
  }

  .group[data-stacked="true"] {
    flex-direction: column;
    gap: var(--space-1);
  }

  legend {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    padding: 0;
    margin-block-end: var(--space-2);
  }

  .radio {
    display: flex;
    align-items: start;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding-block: var(--space-2);
    cursor: pointer;
  }

  input {
    position: absolute;
    opacity: 0;
    inline-size: 1.25rem;
    block-size: 1.25rem;
    margin: 0;
  }

  /* A square radio, filled with a solid block when chosen: the flat language
     has no circles. */
  .mark {
    flex: 0 0 auto;
    inline-size: 1.25rem;
    block-size: 1.25rem;
    margin-block-start: 0.15em;
    background: var(--c-surface);
    box-shadow: 0 0 0 2px var(--c-control-border);
  }

  input:checked + .mark {
    box-shadow: 0 0 0 2px var(--c-blue);
    background:
      linear-gradient(var(--c-blue), var(--c-blue)) center / 0.6rem 0.6rem no-repeat,
      var(--c-surface);
  }

  input:focus-visible + .mark {
    box-shadow: var(--focus-shadow);
  }

  .label {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
    line-height: var(--lh-snug);
  }

  .hint {
    display: block;
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }
</style>
