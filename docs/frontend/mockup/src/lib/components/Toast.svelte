<script lang="ts">
  import type { Snippet } from "svelte";

  interface Item {
    id: string;
    tone?: "neutral" | "success" | "warning";
    message: string;
    /** Optional single recovery action. Give it an href for navigation or an
        onclick for an in-page effect; a label with neither is a dead end, so
        the component refuses to render it as a link. */
    action?: { label: string; href?: string; onclick?: () => void };
  }

  interface Props {
    items: Item[];
    ondismiss?: (id: string) => void;
    children?: Snippet;
  }

  let { items, ondismiss, children }: Props = $props();
</script>

<!-- One live region for the whole app: politeness is polite because a toast
     never blocks reading. Errors that must interrupt use a Banner instead. -->
<div class="region" role="status" aria-live="polite" aria-atomic="false">
  {#each items as item (item.id)}
    <div class="toast" data-tone={item.tone ?? "neutral"}>
      <span class="marker" aria-hidden="true"></span>
      <p class="message">{item.message}</p>
      {#if item.action?.href}
        <a class="action" href={item.action.href}>{item.action.label}</a>
      {:else if item.action}
        <button type="button" class="action" onclick={item.action.onclick}>
          {item.action.label}
        </button>
      {/if}
      <button type="button" class="dismiss" aria-label="Dismiss" onclick={() => ondismiss?.(item.id)}>
        <span aria-hidden="true">×</span>
      </button>
    </div>
  {/each}
</div>
{#if children}{@render children()}{/if}

<style>
  .region {
    position: fixed;
    inset-block-end: var(--space-5);
    inset-inline-end: var(--space-5);
    z-index: 50;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    max-inline-size: min(26rem, calc(100vw - 2 * var(--space-5)));
    pointer-events: none;
  }

  .toast {
    --edge: var(--c-blue);
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    padding: var(--space-3) var(--space-4);
    font-size: var(--step--1);
  }

  .toast[data-tone="success"] { --edge: var(--sig-up); }
  .toast[data-tone="warning"] { --edge: var(--sig-down); }

  /* A 6x18 accent bar, not a left border: the toast keeps the same geometry
     as every other floating box. */
  .marker {
    inline-size: 6px;
    block-size: 1.25rem;
    background: var(--edge);
    flex: 0 0 auto;
  }

  .message {
    flex: 1 1 auto;
    min-inline-size: 0;
  }

  /* The recovery action is the point of a toast, so it is a real target: a
     link when it navigates, a button when it acts, never a bare "#". */
  .action {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    min-block-size: var(--touch-target);
    padding: 0;
    background: none;
    border: 0;
    color: var(--c-text-accent-strong);
    font: inherit;
    text-decoration: underline;
    text-underline-offset: 2px;
    cursor: pointer;
  }

  .dismiss {
    inline-size: var(--touch-target);
    block-size: var(--touch-target);
    display: grid;
    place-items: center;
    margin-inline-end: calc(var(--space-3) * -1);
    background: none;
    border: 0;
    cursor: pointer;
    font-size: var(--step-2);
    line-height: 1;
  }
</style>
