<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Side the panel enters from. Filters use `end` on wide viewports. */
    side?: "start" | "end";
    title: string;
    open?: boolean;
    footer?: Snippet;
    children: Snippet;
  }

  let { side = "end", title, open = $bindable(false), footer, children }: Props = $props();

  let dialog: HTMLDialogElement | undefined = $state();
  const uid = $props.id();
  const titleId = `drawer-title-${uid}`;

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  });
</script>

<dialog
  bind:this={dialog}
  aria-labelledby={titleId}
  onclose={() => (open = false)}
  class="drawer"
  data-side={side}
>
  <div class="inner">
    <header>
      <h2 id={titleId}>{title}</h2>
      <button type="button" class="close" aria-label="Close panel" onclick={() => (open = false)}>
        <span aria-hidden="true">×</span>
      </button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}
      <div class="footer">{@render footer()}</div>
    {/if}
  </div>
</dialog>

<style>
  dialog.drawer {
    /* Full height, docked to one edge, no radius, one hard shadow on the
       inner edge. */
    margin: 0;
    padding: 0;
    border: 0;
    max-inline-size: min(26rem, 100vw);
    inline-size: 100%;
    block-size: 100vh;
    max-block-size: none;
    background: var(--c-surface);
    color: var(--c-text);
    box-shadow: 0 0 0 2px var(--c-blue);
  }

  dialog.drawer[data-side="end"] { margin-inline-start: auto; }
  dialog.drawer[data-side="start"] { margin-inline-end: auto; }

  dialog.drawer::backdrop {
    background: var(--c-scrim);
  }

  .inner {
    display: flex;
    flex-direction: column;
    block-size: 100%;
    padding: var(--space-5);
    overflow: auto;
  }

  header {
    display: flex;
    align-items: start;
    gap: var(--space-4);
    border-block-end: 2px solid var(--c-rule-strong);
    padding-block-end: var(--space-3);
  }

  h2 {
    flex: 1 1 auto;
    font-size: var(--step-2);
  }

  .close {
    inline-size: var(--touch-target);
    block-size: var(--touch-target);
    display: grid;
    place-items: center;
    margin: calc(var(--space-3) * -1) calc(var(--space-3) * -1) 0 0;
    background: none;
    border: 0;
    cursor: pointer;
    font-size: var(--step-2);
    line-height: 1;
  }

  .body {
    flex: 1 1 auto;
    padding-block: var(--space-5);
  }

  .footer {
    border-block-start: 2px solid var(--c-rule);
    padding-block-start: var(--space-4);
  }
</style>
