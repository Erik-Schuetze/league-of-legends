<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Visible trigger label. Icon-only triggers must pass ariaLabel. */
    label: string;
    ariaLabel?: string;
    /** Where the panel hangs. */
    align?: "start" | "end";
    children: Snippet;
  }

  let { label, ariaLabel = undefined, align = "end", children }: Props = $props();

  /* The native Popover API gives light-dismiss, Escape and top-layer stacking
     without a positioning library. */
  const uid = $props.id();
  const id = `popover-${uid}`;
  /* Anchor positioning is what pins the panel to its trigger; without it the
     panel would land in the middle of the viewport. */
  const anchor = `--anchor-${id}`;
</script>

<button
  type="button"
  class="trigger"
  popovertarget={id}
  aria-label={ariaLabel}
  style="anchor-name: {anchor}"
>
  {label}
  <span class="caret" aria-hidden="true">▾</span>
</button>

<div {id} popover class="panel" data-align={align} style="position-anchor: {anchor}">
  {@render children()}
</div>

<style>
  .trigger {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-3);
    background: var(--c-surface);
    border: 0;
    box-shadow: var(--ring);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    cursor: pointer;
  }

  .trigger:hover {
    box-shadow: var(--ring), 4px 4px 0 var(--c-blue);
  }

  .caret {
    font-size: 0.75em;
    color: var(--c-blue);
  }

  .panel {
    margin: 0;
    padding: var(--space-3);
    background: var(--c-surface);
    border: 0;
    box-shadow: var(--shadow-card);
    position-area: bottom span-right;
    position-try-fallbacks: bottom span-left, top span-right;
  }

  .panel[data-align="start"] {
    position-area: bottom span-left;
    position-try-fallbacks: bottom span-right, top span-left;
  }

  .panel:popover-open {
    margin-block-start: var(--space-2);
  }
</style>
