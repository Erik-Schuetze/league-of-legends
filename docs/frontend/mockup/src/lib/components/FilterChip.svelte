<script lang="ts">
  interface Props {
    /** What is being filtered, e.g. "Role". */
    label: string;
    /** What it is set to, e.g. "MID". */
    value: string;
    onremove?: () => void;
  }

  let { label, value, onremove }: Props = $props();
</script>

<span class="filter-chip">
  <span class="key">{label}</span>
  <span class="value">{value}</span>
  <button type="button" onclick={() => onremove?.()}>
    <span class="visually-hidden">Remove filter {label}: {value}</span>
    <span aria-hidden="true">×</span>
  </button>
</span>

<style>
  .filter-chip {
    display: inline-flex;
    align-items: stretch;
    background: var(--c-surface);
    /* A filter in force is a raised paper token: ring plus offset, like every
       other box. It is not tinted, because tint means "selected row". */
    box-shadow: var(--ring), 3px 3px 0 var(--c-blue);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    white-space: nowrap;
  }

  .key {
    display: inline-flex;
    align-items: center;
    padding: var(--space-1) var(--space-2);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    border-inline-end: 2px solid var(--c-rule);
  }

  .value {
    display: inline-flex;
    align-items: center;
    padding: var(--space-1) var(--space-2);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
  }

  button {
    display: grid;
    place-items: center;
    inline-size: 2rem;
    min-block-size: 2rem;
    background: none;
    border: 0;
    border-inline-start: 2px solid var(--c-rule);
    cursor: pointer;
    color: var(--c-text-strong);
    font-size: var(--step-0);
    line-height: 1;
  }

  @media (max-width: 767px) {
    button {
      inline-size: var(--touch-target);
      min-block-size: var(--touch-target);
    }
  }

  button:hover {
    background: var(--c-blue);
    color: var(--c-cream);
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
