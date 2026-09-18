<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Inset surface for code, formulae, legends and nested content. */
    tone?: "sunken" | "paper";
    labelledby?: string;
    children: Snippet;
  }

  let { tone = "sunken", labelledby = undefined, children }: Props = $props();
</script>

<div
  class="well"
  class:paper={tone === "paper"}
  aria-labelledby={labelledby}
>
  {@render children()}
</div>

<style>
  .well {
    background: var(--c-surface-sunken);
    box-shadow: var(--shadow-inset);
    padding: var(--space-4);
  }

  /* A well inside a card would otherwise place sand on cream on sand. */
  .well.paper {
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }
</style>
