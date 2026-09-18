<script lang="ts">
  interface Props {
    checked?: boolean;
    label: string;
    hint?: string;
    /** Text for the two positions. Defaults are neutral rather than "on/off"
     *  so the switch can carry meaning like "include suppressed". */
    onLabel?: string;
    offLabel?: string;
    disabled?: boolean;
    onchange?: (checked: boolean) => void;
  }

  let {
    checked = $bindable(false),
    label,
    hint = undefined,
    onLabel = "on",
    offLabel = "off",
    disabled = false,
    onchange,
  }: Props = $props();
</script>

<label class="switch">
  <input type="checkbox" bind:checked {disabled} onchange={() => onchange?.(checked)} />
  <span class="track" aria-hidden="true"><span class="thumb"></span></span>
  <span class="text">
    <span class="label">{label}</span>
    <span class="state">
      <span class="num">{checked ? onLabel : offLabel}</span>
      {#if hint}
        <span class="hint">· {hint}</span>
      {/if}
    </span>
  </span>
</label>

<style>
  .switch {
    display: flex;
    align-items: start;
    gap: var(--space-3);
    min-block-size: var(--touch-target);
    padding-block: var(--space-2);
    cursor: pointer;
  }

  input {
    position: absolute;
    opacity: 0;
  }

  /* Square track, square thumb, no rounding, no transition on the thumb's
     shape: it jumps, like a switch on a machine. */
  .track {
    flex: 0 0 auto;
    inline-size: 2.75rem;
    block-size: 1.5rem;
    margin-block-start: 0.1em;
    background: var(--c-surface);
    box-shadow: 0 0 0 2px var(--c-control-border);
    display: flex;
    align-items: center;
    padding: 2px;
  }

  .thumb {
    inline-size: 1rem;
    block-size: 1rem;
    background: var(--c-control-border);
    transition: translate var(--dur-fast) var(--ease);
  }

  input:checked + .track {
    box-shadow: 0 0 0 2px var(--c-blue);
    background: var(--tint-accent-strong);
  }

  input:checked + .track .thumb {
    translate: 1.25rem 0;
    background: var(--c-blue);
  }

  input:focus-visible + .track {
    box-shadow: var(--focus-shadow);
  }

  input:disabled + .track {
    box-shadow: 0 0 0 2px var(--c-rule);
  }

  .label {
    display: block;
    font-size: var(--step--1);
    color: var(--c-text-strong);
    line-height: var(--lh-snug);
  }

  .state {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  @media (prefers-reduced-motion: reduce) {
    .thumb { transition: none; }
  }
</style>
