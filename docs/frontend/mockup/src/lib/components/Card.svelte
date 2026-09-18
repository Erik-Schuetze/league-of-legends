<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Rendered element. Use "section"/"article" when the card has a heading,
     *  "li" inside a list, "div" for a pure layout box. */
    as?: "div" | "section" | "article" | "li" | "aside";
    /** Hover lift plus pointer cursor. Only for cards that are links or
     *  buttons themselves. */
    interactive?: boolean;
    /** Removes padding when the card hosts an edge-to-edge table. */
    flush?: boolean;
    labelledby?: string;
    children: Snippet;
  }

  let {
    as = "div",
    interactive = false,
    flush = false,
    labelledby = undefined,
    children,
  }: Props = $props();
</script>

<svelte:element
  this={as}
  class="card"
  class:interactive
  class:flush
  aria-labelledby={labelledby}
>
  {@render children()}
</svelte:element>

<style>
  .card {
    position: relative;
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    padding: var(--space-5);
  }

  .card.flush {
    padding: 0;
  }

  .card.interactive {
    cursor: pointer;
    transition:
      transform var(--dur-base) var(--ease-out),
      box-shadow var(--dur-base) var(--ease-out);
  }

  /* The element travels toward its own shadow: the shadow shortens, it does not
     grow. Never add a blurred shadow here. */
  .card.interactive:hover,
  .card.interactive:focus-visible {
    transform: var(--lift);
    box-shadow:
      var(--ring),
      var(--offset-hover);
  }
</style>
