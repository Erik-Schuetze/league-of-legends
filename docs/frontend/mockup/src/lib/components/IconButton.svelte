<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Accessible name. Always required: the button has no text. */
    label: string;
    /** Shown as a bubble on hover/focus when provided. */
    hint?: string;
    size?: "sm" | "md";
    pressed?: boolean;
    disabled?: boolean;
    onclick?: (event: MouseEvent) => void;
    children: Snippet;
  }

  let {
    label,
    hint = undefined,
    size = "md",
    pressed = undefined,
    disabled = false,
    onclick,
    children,
  }: Props = $props();
</script>

<button
  type="button"
  class="icon-btn"
  data-size={size}
  aria-label={label}
  aria-pressed={pressed}
  {disabled}
  {onclick}
  title={hint}
>
  {@render children()}
</button>

<style>
  .icon-btn {
    display: grid;
    place-items: center;
    inline-size: var(--touch-target);
    block-size: var(--touch-target);
    padding: 0;
    background: var(--c-surface);
    border: 0;
    box-shadow: var(--ring);
    color: var(--c-text-strong);
    cursor: pointer;
    font-size: var(--step-0);
    line-height: 1;
    transition:
      transform var(--dur-base) var(--ease-out),
      box-shadow var(--dur-base) var(--ease-out);
  }

  .icon-btn[data-size="sm"] {
    inline-size: 2rem;
    block-size: 2rem;
    font-size: var(--step--1);
  }

  @media (max-width: 767px) {
    .icon-btn[data-size="sm"] {
      inline-size: var(--touch-target);
      block-size: var(--touch-target);
    }
  }

  .icon-btn:hover:not(:disabled) {
    transform: var(--lift);
    box-shadow: var(--ring), 4px 4px 0 var(--c-blue);
  }

  /* A pressed icon button is a toggled one: it takes the accent tint, not a
     full blue fill, so it never competes with a primary button. */
  .icon-btn[aria-pressed="true"] {
    background: var(--tint-accent-strong);
    box-shadow: var(--ring), 2px 2px 0 var(--c-blue);
  }

  .icon-btn:disabled {
    color: var(--c-text-disabled);
    box-shadow: 0 0 0 2px var(--c-rule);
    cursor: not-allowed;
  }

  @media (prefers-reduced-motion: reduce) {
    .icon-btn { transition: none; }
    .icon-btn:hover:not(:disabled) { transform: none; }
  }
</style>
