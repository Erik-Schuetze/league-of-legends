<script lang="ts">
  interface Option {
    value: string;
    label: string;
    /** Optional trailing count, e.g. rows in that bucket. */
    count?: number;
  }

  interface Props {
    options: Option[];
    value: string;
    onchange?: (value: string) => void;
    /** Accessible name for the group. Render a visible label with `label`. */
    name: string;
    label?: string;
    size?: "sm" | "md";
  }

  let { options, value = $bindable(), onchange, name, label = undefined, size = "md" }: Props =
    $props();

  function select(next: string) {
    value = next;
    onchange?.(next);
  }
</script>

<div class="segmented-wrap">
  {#if label}
    <p class="label" id={`${name}-label`}>{label}</p>
  {/if}
  <div
    class="segmented"
    role="radiogroup"
    aria-label={label ? undefined : name}
    aria-labelledby={label ? `${name}-label` : undefined}
    data-size={size}
  >
    {#each options as option (option.value)}
      <button
        type="button"
        role="radio"
        aria-checked={value === option.value}
        tabindex={value === option.value ? 0 : -1}
        onclick={() => select(option.value)}
        onkeydown={(e) => {
          if (!["ArrowRight", "ArrowLeft", "ArrowUp", "ArrowDown"].includes(e.key)) return;
          e.preventDefault();
          const i = options.findIndex((o) => o.value === value);
          const step = e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : -1;
          const next = options[(i + step + options.length) % options.length];
          select(next.value);
          (e.currentTarget.parentElement?.querySelector(
            `[data-value="${next.value}"]`,
          ) as HTMLElement | null)?.focus();
        }}
        data-value={option.value}
      >
        <span>{option.label}</span>
        {#if option.count !== undefined}
          <span class="count num">{option.count}</span>
        {/if}
      </button>
    {/each}
  </div>
</div>

<style>
  .segmented {
    display: inline-flex;
    background: var(--c-surface);
    box-shadow: var(--ring);
    padding: 2px;
    gap: 2px;
  }

  button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-1) var(--space-4);
    background: none;
    border: 0;
    cursor: pointer;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-muted);
    white-space: nowrap;
  }

  [data-size="sm"] button {
    min-block-size: 2rem;
    font-size: var(--step--2);
    padding-inline: var(--space-3);
  }

  @media (max-width: 767px) {
    [data-size="sm"] button {
      min-block-size: var(--touch-target);
    }
  }

  button:hover {
    color: var(--c-text-strong);
    background: var(--tint-accent);
  }

  /* The selected segment is a raised paper block inside the ring: it reads as
     the current position without becoming a button. */
  button[aria-checked="true"] {
    background: var(--c-surface-sunken);
    color: var(--c-text-strong);
    box-shadow: 0 0 0 2px var(--c-blue);
  }

  .count {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .label {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    margin-block-end: var(--space-2);
  }
</style>
