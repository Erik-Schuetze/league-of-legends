<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Short text. A tooltip never carries information that exists nowhere
     *  else: it is unavailable to touch and to most assistive tech. */
    text: string;
    children: Snippet;
  }

  let { text, children }: Props = $props();
  const uid = $props.id();
  const id = `tip-${uid}`;</script>

<span class="tip" aria-describedby={id}>
  {@render children()}
  <span class="bubble" role="tooltip" {id} data-tip>{text}</span>
</span>

<style>
  .tip {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  .bubble {
    position: absolute;
    inset-block-end: calc(100% + var(--space-2));
    inset-inline-start: 50%;
    translate: -50% 0;
    z-index: 20;
    inline-size: max-content;
    max-inline-size: 18rem;
    padding: var(--space-2) var(--space-3);
    background: var(--c-surface-inverse);
    color: var(--c-text-on-ink);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    line-height: var(--lh-snug);
    text-align: start;
    opacity: 0;
    visibility: hidden;
    transition: opacity var(--dur-fast) var(--ease);
  }

  .tip:hover .bubble,
  .tip:focus-within .bubble {
    opacity: 1;
    visibility: visible;
  }

  @media (prefers-reduced-motion: reduce) {
    .bubble { transition: none; }
  }
</style>
