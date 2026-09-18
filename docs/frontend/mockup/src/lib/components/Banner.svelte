<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    tone?: "info" | "success" | "warning" | "danger";
    /** Banners sit directly under the nav and describe the whole page. */
    dismissible?: boolean;
    onclose?: () => void;
    children: Snippet;
  }

  let { tone = "info", dismissible = false, onclose, children }: Props = $props();

  const role = $derived(tone === "danger" || tone === "warning" ? "alert" : "status");
</script>

<div class="banner" data-tone={tone} {role}>
  <div class="body">{@render children()}</div>
  {#if dismissible}
    <button
      type="button"
      class="close"
      aria-label="Dismiss this notice"
      onclick={() => onclose?.()}
    >
      <span aria-hidden="true">×</span>
    </button>
  {/if}
</div>

<style>
  .banner {
    --edge: var(--c-blue);
    display: flex;
    align-items: start;
    gap: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--ring), var(--offset-card);
    border-inline-start: 8px solid var(--edge);
    padding: var(--space-3) var(--space-4);
    font-size: var(--step--1);
  }

  .banner[data-tone="success"] { --edge: var(--sig-up); }
  .banner[data-tone="warning"] { --edge: var(--sig-down); }
  .banner[data-tone="danger"] { --edge: var(--c-ink); }

  .body { flex: 1 1 auto; min-width: 0; }

  .close {
    flex: none;
    inline-size: var(--touch-target);
    block-size: var(--touch-target);
    margin: calc(var(--space-2) * -1) calc(var(--space-2) * -1) 0 0;
    display: grid;
    place-items: center;
    background: none;
    border: 0;
    cursor: pointer;
    font-size: var(--step-2);
    line-height: 1;
    color: var(--c-text-strong);
  }
</style>
