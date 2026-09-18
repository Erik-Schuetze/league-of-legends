<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    open?: boolean;
    title: string;
    /** One line of context. A dialog that needs three lines needs a
     *  full page instead. */
    description?: string;
    footer?: Snippet;
    children: Snippet;
  }

  let { open = $bindable(false), title, description = undefined, footer, children }: Props =
    $props();

  let dialog: HTMLDialogElement | undefined = $state();
  const uid = $props.id();
  const titleId = `dialog-title-${uid}`;
  const descId = `dialog-desc-${uid}`;

  /* showModal() is what gives us focus trapping, Escape handling and the
     inert backdrop for free. Do not build a div-based dialog. */
  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  });

  function onclose() {
    open = false;
  }
</script>

<dialog
  bind:this={dialog}
  aria-labelledby={titleId}
  aria-describedby={description ? descId : undefined}
  onclose={onclose}
  oncancel={(e) => {
    /* Escape is allowed: nothing in this product loses work on close. */
    e.preventDefault();
    open = false;
    dialog?.close();
  }}
>
  <div class="inner">
    <header>
      <h2 id={titleId}>{title}</h2>
      <button type="button" class="close" aria-label="Close dialog" onclick={() => (open = false)}>
        <span aria-hidden="true">×</span>
      </button>
    </header>
    {#if description}
      <p id={descId} class="description">{description}</p>
    {/if}
    <div class="body">{@render children()}</div>
    {#if footer}
      <div class="footer">{@render footer()}</div>
    {/if}
  </div>
</dialog>

<style>
  dialog {
    inline-size: min(38rem, calc(100vw - 2 * var(--space-5)));
    max-block-size: calc(100vh - 2 * var(--space-6));
    margin: auto;
    padding: 0;
    border: 0;
    background: var(--c-surface);
    box-shadow: var(--shadow-hero);
    color: var(--c-text);
  }

  dialog::backdrop {
    background: var(--c-scrim);
  }

  .inner {
    padding: var(--space-5);
  }

  header {
    display: flex;
    align-items: start;
    gap: var(--space-4);
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

  .description {
    margin-block-start: var(--space-3);
    max-width: var(--measure-narrow);
    color: var(--c-text-muted);
  }

  .body {
    margin-block-start: var(--space-5);
  }

  .footer {
    margin-block-start: var(--space-6);
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    justify-content: flex-end;
    border-block-start: 2px solid var(--c-rule);
    padding-block-start: var(--space-4);
  }

  @media (forced-colors: active) {
    dialog { border: 2px solid CanvasText; }
  }
</style>
