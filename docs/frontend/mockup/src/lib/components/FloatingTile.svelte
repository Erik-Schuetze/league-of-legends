<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Interior size. 150px is the measured reference tile. */
    size?: number;
    /** Decorative tiles drift. Never float anything interactive. */
    float?: boolean;
    /** Stagger the drift so a row of tiles does not move in lockstep. */
    phase?: "a" | "b" | "c";
    /** Absence of elevation: a sunken well instead of a floating box. */
    sunken?: boolean;
    label?: string;
    children?: Snippet;
  }

  let {
    size = 150,
    float = false,
    phase = "a",
    sunken = false,
    label = undefined,
    children,
  }: Props = $props();
</script>

<div
  class="tile"
  class:float
  class:sunken
  style="--tile-size: {size}px"
  data-phase={phase}
  aria-label={label}
  role={label ? "img" : undefined}
>
  {#if children}
    <div class="interior">{@render children()}</div>
  {/if}
</div>

<style>
  .tile {
    inline-size: var(--tile-size);
    block-size: var(--tile-size);
    flex: 0 0 auto;
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    display: grid;
    place-items: center;
  }

  .tile.sunken {
    box-shadow: var(--shadow-inset);
    background: var(--c-surface-sunken);
    color: var(--c-text-muted);
  }

  .interior {
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    font-size: var(--step-2);
    line-height: 1;
    letter-spacing: var(--ls-tight);
    text-align: center;
    padding: var(--space-3);
  }

  /* Decorative drift only. Disabled wholesale by the reduced-motion token
     block: --float-amp becomes 0px, which makes this keyframe a no-op. */
  .tile.float {
    animation: drift var(--float-dur-a) var(--ease) infinite;
  }
  .tile.float[data-phase="b"] { animation-duration: var(--float-dur-b); }
  .tile.float[data-phase="c"] { animation-duration: var(--float-dur-c); }

  @keyframes drift {
    0%, 100% { translate: 0 0; }
    50% { translate: 0 var(--float-amp); }
  }

  @media (prefers-reduced-motion: reduce) {
    .tile.float { animation: none; }
  }

  @media (forced-colors: active) {
    .tile { border: 2px solid CanvasText; }
  }
</style>
