<script lang="ts">
  interface Props {
    checked?: boolean;
    /** Visible label text. Every checkbox has one: a checkbox that needs a
     *  tooltip to be understood is a design fault. */
    label: string;
    /** Second line: what checking it does. */
    hint?: string;
    /** Count of rows this filter would leave, shown right-aligned. */
    count?: number;
    name?: string;
    value?: string;
    disabled?: boolean;
    onchange?: (checked: boolean) => void;
  }

  let {
    checked = $bindable(false),
    label,
    hint = undefined,
    count = undefined,
    name = undefined,
    value = undefined,
    disabled = false,
    onchange,
  }: Props = $props();
</script>

<label class="check">
  <input
    type="checkbox"
    bind:checked
    {name}
    {value}
    {disabled}
    onchange={() => onchange?.(checked)}
  />
  <span class="box" aria-hidden="true"></span>
  <span class="text">
    <span class="label">{label}</span>
    {#if hint}
      <span class="hint">{hint}</span>
    {/if}
  </span>
  {#if count !== undefined}
    <span class="count num">{count.toLocaleString("en-GB")}</span>
  {/if}
</label>

<style>
  .check {
    display: flex;
    align-items: start;
    gap: var(--space-3);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-2) var(--space-2) 0;
    cursor: pointer;
  }

  input {
    position: absolute;
    opacity: 0;
    inline-size: 1.25rem;
    block-size: 1.25rem;
    margin: 0;
  }

  /* The mark is a square with the same 2px ring as every other control. */
  .box {
    flex: 0 0 auto;
    inline-size: 1.25rem;
    block-size: 1.25rem;
    margin-block-start: 0.15em;
    background: var(--c-surface);
    box-shadow: 0 0 0 2px var(--c-control-border);
    display: grid;
    place-items: center;
  }

  input:checked + .box {
    background: var(--c-blue);
    box-shadow: 0 0 0 2px var(--c-blue);
  }

  input:checked + .box::after {
    content: "";
    /* A hard check drawn with two borders: no icon font, no SVG. */
    inline-size: 0.4rem;
    block-size: 0.65rem;
    border: solid var(--c-cream);
    border-width: 0 2px 2px 0;
    translate: 0 -0.1rem;
    rotate: 45deg;
  }

  input:focus-visible + .box {
    box-shadow: var(--focus-shadow);
  }

  input:disabled ~ * {
    color: var(--c-text-disabled);
  }

  input:disabled + .box {
    box-shadow: 0 0 0 2px var(--c-rule);
    background: var(--c-surface-sunken);
  }

  .text {
    flex: 1 1 auto;
    min-inline-size: 0;
  }

  .label {
    display: block;
    font-size: var(--step--1);
    color: var(--c-text-strong);
    line-height: var(--lh-snug);
  }

  .hint {
    display: block;
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .count {
    flex: 0 0 auto;
    font-size: var(--step--2);
    color: var(--c-text-muted);
    align-self: center;
  }
</style>
