<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Component file name, so a reader can find the source. */
    name: string;
    /** Which state or variant this instance shows. */
    state?: string;
    /** One line on what to look for. */
    note?: string;
    /** Tones the frame when the specimen shows a withheld or failed state. */
    flag?: "honesty" | "none";
    children: Snippet;
  }

  let { name, state = undefined, note = undefined, flag = "none", children }: Props = $props();
</script>

<section class="specimen" data-flag={flag}>
  <header class="head">
    <p class="id">
      <span class="file num">{name}</span>
      {#if state}<span class="state">{state}</span>{/if}
    </p>
    {#if note}<p class="note">{note}</p>{/if}
  </header>
  <div class="body">
    {@render children()}
  </div>
</section>

<style>
  .specimen {
    display: grid;
    gap: var(--space-3);
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }

  /* A specimen that shows a withheld or failed state is marked, so the gallery
     does not read as if it were itself broken. */
  .specimen[data-flag="honesty"] {
    border-inline-start: 6px solid var(--sig-unknown);
  }

  .head {
    display: grid;
    gap: var(--space-1);
  }

  .id {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-3);
    margin: 0;
  }

  .file {
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    color: var(--c-text-accent-strong);
  }

  .state {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .note {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
    max-inline-size: 70ch;
  }

  .body {
    display: grid;
    gap: var(--space-4);
    align-content: start;
  }
</style>
