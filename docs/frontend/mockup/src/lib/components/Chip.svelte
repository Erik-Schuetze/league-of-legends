<script lang="ts">
  interface Props {
    label: string;
    /** A chip is a value, not an action. Rendered as a boxed tag. */
    tone?: "neutral" | "accent" | "up" | "down" | "unknown";
    size?: "sm" | "md";
    /** Optional trailing figure, right-aligned in mono. */
    value?: string;
  }

  let { label, tone = "neutral", size = "md", value = undefined }: Props = $props();
</script>

<span class="chip" data-tone={tone} data-size={size}>
  {label}
  {#if value}<span class="value num">{value}</span>{/if}
</span>

<style>
  .chip {
    --edge: var(--c-control-border);
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) var(--space-2);
    background: var(--c-surface);
    box-shadow: 0 0 0 2px var(--edge);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-strong);
    white-space: nowrap;
  }

  .chip[data-size="sm"] {
    padding: 1px var(--space-1);
    font-size: 0.625rem;
  }

  .chip[data-tone="accent"] { --edge: var(--c-blue); color: var(--c-text-accent-strong); }
  .chip[data-tone="up"] { --edge: var(--sig-up); color: var(--sig-up); }
  .chip[data-tone="down"] { --edge: var(--sig-down); color: var(--sig-down); }
  .chip[data-tone="unknown"] { --edge: var(--sig-unknown); color: var(--sig-unknown); }

  .value {
    font-size: 0.95em;
    color: var(--c-text-strong);
  }
</style>
