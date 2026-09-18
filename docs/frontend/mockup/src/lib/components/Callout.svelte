<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** The meaning of the box. `honesty` is reserved for data caveats and is
     *  the only variant allowed to state a limitation. */
    tone?: "note" | "caution" | "honesty";
    /** Short uppercase label rendered above the body. */
    label: string;
    /** Optional action row, usually a link or a button. */
    actions?: Snippet;
    children: Snippet;
  }

  let { tone = "note", label, actions, children }: Props = $props();

  const glyph = { note: "i", caution: "!", honesty: "n" } as const;
</script>

<div class="callout" data-tone={tone} role={tone === "caution" ? "note" : undefined}>
  <span class="glyph num" aria-hidden="true">{glyph[tone]}</span>
  <div class="body">
    <p class="label">{label}</p>
    <div class="content prose">{@render children()}</div>
    {#if actions}
      <div class="actions">{@render actions()}</div>
    {/if}
  </div>
</div>

<style>
  .callout {
    --edge: var(--c-ink);
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--space-4);
    align-items: start;
    background: var(--c-surface);
    box-shadow:
      var(--ring),
      var(--offset-card);
    padding: var(--space-4) var(--space-5);
  }

  .callout[data-tone="caution"] {
    --edge: var(--sig-down);
  }

  /* Honesty notes are about sample size and coverage, so they wear the teal
     used for sample annotations. */
  .callout[data-tone="honesty"] {
    --edge: var(--sig-up);
  }

  .glyph {
    display: grid;
    place-items: center;
    inline-size: 1.75rem;
    block-size: 1.75rem;
    background: var(--edge);
    color: var(--c-cream);
    font-weight: var(--fw-bold);
    font-size: var(--step--1);
    line-height: 1;
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

  /* A grid gap spaces the projected children without a child selector: the
     content arrives as a snippet, so its elements are not visible to the
     component's scoped styles. */
  .content {
    max-width: var(--measure);
    display: grid;
    gap: var(--space-3);
    align-content: start;
  }

  .actions {
    margin-block-start: var(--space-3);
  }

  .content :global(> * + *) {
    margin-block-start: var(--space-3);
  }
</style>
