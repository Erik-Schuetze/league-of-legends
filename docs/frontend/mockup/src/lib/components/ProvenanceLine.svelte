<script lang="ts">
  interface Props {
    /** Where the numbers came from. "demo" and "synthetic" are shown loudly;
     *  "riot" keeps the line quiet but still present. */
    source: string;
    /** Free-text provenance, e.g. the ingest job that produced the build. */
    detail?: string;
    variant?: "inline" | "block";
  }

  let { source, detail = undefined, variant = "inline" }: Props = $props();

  const loud = $derived(source === "demo" || source === "synthetic" || source === "fixture");

  const word = $derived(
    loud
      ? `This is ${source} data, not real match records`
      : `Source: ${source}`,
  );
</script>

<p class="provenance" data-loud={loud} data-variant={variant}>
  <span class="glyph" aria-hidden="true">{loud ? "⚑" : "◦"}</span>
  <span class="word">{word}</span>
  {#if detail}
    <span class="detail">{detail}</span>
  {/if}
</p>

<style>
  .provenance {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .provenance[data-variant="block"] {
    padding: var(--space-3) var(--space-4);
    background: var(--c-surface);
    border-inline-start: 6px solid var(--sig-unknown);
  }

  .glyph {
    font-family: var(--font-mono);
  }

  .word {
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
  }

  /* Fixture data is a label, not an error: amber-free, no alarm, but it does
     not look like an ordinary footnote either. */
  .provenance[data-loud="true"] .word,
  .provenance[data-loud="true"] .glyph {
    color: var(--sig-unknown);
  }

  .provenance[data-loud="true"] .word {
    background: var(--sig-unknown-tint);
    padding-inline: 0.3em;
  }

  .detail {
    color: var(--c-text-muted);
  }
</style>
