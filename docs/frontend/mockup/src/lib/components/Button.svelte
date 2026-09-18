<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /**
     * primary   one per view: the action the page exists for
     * secondary the default: a real action, quieter than primary
     * ghost     in-flow actions inside rows and toolbars
     * danger    destructive; always paired with a confirmation
     */
    variant?: "primary" | "secondary" | "ghost" | "danger";
    size?: "sm" | "md";
    /** Renders an anchor with button styling. Use for navigation. */
    href?: string;
    /** Shows progress and blocks the click. Keep the label. */
    busy?: boolean;
    disabled?: boolean;
    type?: "button" | "submit" | "reset";
    /** Required when the button's only content is an icon. */
    ariaLabel?: string;
    onclick?: (event: MouseEvent) => void;
    children: Snippet;
  }

  let {
    variant = "secondary",
    size = "md",
    href = undefined,
    busy = false,
    disabled = false,
    type = "button",
    ariaLabel = undefined,
    onclick,
    children,
  }: Props = $props();

  const isDisabled = $derived(disabled || busy);
</script>

{#if href && !isDisabled}
  <a class="btn" data-variant={variant} data-size={size} {href} aria-label={ariaLabel}>
    {@render children()}
  </a>
{:else if href}
  <span class="btn" data-variant={variant} data-size={size} aria-disabled="true" aria-label={ariaLabel}>
    {@render children()}
  </span>
{:else}
  <button
    class="btn"
    data-variant={variant}
    data-size={size}
    {type}
    disabled={isDisabled}
    aria-busy={busy}
    aria-label={ariaLabel}
    {onclick}
  >
    {@render children()}
  </button>
{/if}

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-5);
    background: var(--c-surface);
    color: var(--c-text-strong);
    border: 0;
    box-shadow: var(--ring), 4px 4px 0 var(--c-blue);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    letter-spacing: 0.01em;
    text-decoration: none;
    cursor: pointer;
    transition:
      transform var(--dur-base) var(--ease-out),
      box-shadow var(--dur-base) var(--ease-out);
  }

  .btn[data-size="sm"] {
    min-block-size: 2.25rem;
    padding: var(--space-1) var(--space-3);
    font-size: var(--step--2);
  }

  /* On a phone the touch floor wins over the compact desktop size: the label
     still shrinks, the box does not. */
  @media (max-width: 767px) {
    .btn[data-size="sm"] {
      min-block-size: var(--touch-target);
    }
  }

  .btn:hover:not([disabled]):not([aria-disabled="true"]) {
    transform: var(--lift);
    box-shadow: var(--ring), 6px 6px 0 var(--c-blue);
  }

  .btn:active:not([disabled]):not([aria-disabled="true"]) {
    transform: translate(2px, 2px);
    box-shadow: var(--ring), 2px 2px 0 var(--c-blue);
  }

  .btn:disabled,
  .btn[aria-disabled="true"] {
    cursor: not-allowed;
    color: var(--c-text-disabled);
    box-shadow: 0 0 0 2px var(--c-rule);
  }

  /* Primary: accent fill, hard ink offset. Ink, not blue, so the fill and the
     offset never read as one shape. */
  .btn[data-variant="primary"] {
    background: var(--c-blue);
    color: var(--c-text-on-accent);
    box-shadow: var(--ring), 4px 4px 0 var(--c-ink);
  }
  .btn[data-variant="primary"]:hover:not([disabled]):not([aria-disabled="true"]) {
    box-shadow: var(--ring), 6px 6px 0 var(--c-ink);
  }
  .btn[data-variant="primary"]:disabled {
    background: var(--c-surface);
  }

  /* Ghost: no box at all until you touch it. */
  .btn[data-variant="ghost"] {
    background: none;
    box-shadow: none;
    padding-inline: var(--space-3);
    color: var(--c-text-accent);
    text-decoration: underline;
    text-underline-offset: 0.18em;
  }
  .btn[data-variant="ghost"]:hover:not([disabled]) {
    transform: none;
    background: var(--tint-accent);
    box-shadow: none;
    text-decoration-thickness: 2px;
  }
  .btn[data-variant="ghost"]:disabled {
    box-shadow: none;
    background: none;
  }

  .btn[data-variant="danger"] {
    background: var(--sig-down);
    color: var(--c-text-on-down);
    box-shadow: var(--ring), 4px 4px 0 var(--c-ink);
  }
  .btn[data-variant="danger"]:hover:not([disabled]) {
    box-shadow: var(--ring), 6px 6px 0 var(--c-ink);
  }
  .btn[data-variant="danger"]:disabled {
    background: var(--c-surface);
  }

  /* Busy keeps the geometry and adds a stepping block, so nothing shifts. */
  .btn[aria-busy="true"]::after {
    content: "";
    inline-size: 0.75rem;
    block-size: 0.75rem;
    background: currentColor;
    animation: pulse 1s steps(2, end) infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.2; }
  }

  @media (prefers-reduced-motion: reduce) {
    .btn { transition: none; }
    .btn:hover:not([disabled]) { transform: none; }
    .btn[aria-busy="true"]::after { animation: none; }
  }
</style>
