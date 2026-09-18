<script lang="ts">
  interface Props {
    generatedAt: string;
    /** How often a fresh build is expected, in days. Default is a weekly cadence. */
    cadenceDays?: number;
    /** Overrides the clock. The gallery uses this to show every state at once;
     *  pages never pass it. */
    now?: number;
    variant?: "inline" | "block";
  }

  let { generatedAt, cadenceDays = 7, now = undefined, variant = "inline" }: Props = $props();

  /* The clock is read after hydration, never during prerender: a build-time
     age baked into static HTML would be wrong the moment it ships, and reading
     the clock in both passes would mismatch. */
  // svelte-ignore state_referenced_locally
  // `now` is an override for the gallery; pages leave it undefined and the
  // effect below reads the real clock once, after hydration.
  let ageReference: number | undefined = $state(now);
  const built = $derived(new Date(generatedAt).getTime());

  $effect(() => {
    if (now === undefined) ageReference = Date.now();
  });

  const ageDays: number = $derived(((ageReference ?? built) - built) / 86_400_000);

  /* Three states, three different amounts of urgency. Never colour alone: each
     state carries a glyph and a word as well. */
  const freshness = $derived(
    Number.isNaN(ageDays)
      ? "unknown"
      : ageDays <= cadenceDays
        ? "fresh"
        : ageDays <= cadenceDays * 3
          ? "aging"
          : "stale",
  );

  const glyph = $derived(freshness === "fresh" ? "●" : freshness === "aging" ? "◐" : freshness === "stale" ? "◌" : "?");

  const word = $derived(
    freshness === "fresh"
      ? "Current build"
      : freshness === "aging"
        ? "Behind cadence"
        : freshness === "stale"
          ? "Stale build"
          : "Build age unknown",
  );

  const detail = $derived(
    freshness === "fresh"
      ? `Built ${Math.max(0, Math.floor(ageDays))} day(s) ago, within the ${cadenceDays}-day cadence.`
      : freshness === "aging"
        ? `Built ${Math.floor(ageDays)} days ago: later than the ${cadenceDays}-day cadence, but not abandoned.`
        : freshness === "stale"
          ? `Built ${Math.floor(ageDays)} days ago. Figures in this view may describe an older patch.`
          : "The build timestamp could not be read, so freshness is unknown.",
  );
</script>

<p class="staleness" data-state={freshness} data-variant={variant} title={detail}>
  <span class="glyph" aria-hidden="true">{glyph}</span>
  <span class="word">{word}</span>
  <span class="detail">{detail}</span>
</p>

<style>
  .staleness {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .staleness[data-variant="block"] {
    padding: var(--space-3) var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
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

  .detail {
    color: var(--c-text-muted);
  }

  .staleness[data-state="fresh"] .glyph,
  .staleness[data-state="fresh"] .word {
    color: var(--sig-up);
  }

  .staleness[data-state="aging"] .glyph,
  .staleness[data-state="aging"] .word {
    color: var(--sig-down);
  }

  .staleness[data-state="stale"] .glyph,
  .staleness[data-state="stale"] .word {
    color: var(--sig-down);
  }

  .staleness[data-state="stale"] .word {
    background: var(--sig-down-tint);
  }

  .staleness[data-state="unknown"] .glyph,
  .staleness[data-state="unknown"] .word {
    color: var(--sig-unknown);
  }
</style>
