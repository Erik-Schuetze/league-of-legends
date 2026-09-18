<script lang="ts">
  interface Props {
    /** Plain-language headline. Never a status code on its own. */
    title?: string;
    /** What happened, what it means for the data on screen, and what is safe
     *  to assume. Write all three. */
    body: string;
    /** Technical detail, shown in a disclosure. Useful for a bug report. */
    detail?: string;
    /** Retry is offered when retrying can plausibly help. */
    onretry?: () => void;
    /** Escape hatch: the last good view, or a smaller slice that works. */
    fallbackHref?: string;
    fallbackLabel?: string;
    /** Marks the surrounding data as untrustworthy, which is the point. */
    staleData?: boolean;
  }

  let {
    title = "This view could not be loaded",
    body,
    detail = undefined,
    onretry = undefined,
    fallbackHref = undefined,
    fallbackLabel = undefined,
    staleData = false,
  }: Props = $props();

  let showDetail = $state(false);
</script>

<div class="error" role="alert">
  <p class="stamp">
    <span class="glyph" aria-hidden="true">✕</span> Error
  </p>
  <h3 class="heading">{title}</h3>
  <p class="body">{body}</p>
  {#if staleData}
    <p class="caveat">
      Anything still on screen from before this failure is the last successfully built state, not the
      current one.
    </p>
  {/if}
  <div class="actions">
    {#if onretry}
      <button type="button" class="retry" onclick={onretry}>Try again</button>
    {/if}
    {#if fallbackHref && fallbackLabel}
      <a class="fallback" href={fallbackHref}>{fallbackLabel}</a>
    {/if}
    {#if detail}
      <button type="button" class="toggle" aria-expanded={showDetail} onclick={() => (showDetail = !showDetail)}>
        {showDetail ? "Hide" : "Show"} technical detail
      </button>
    {/if}
  </div>
  {#if detail && showDetail}
    <pre class="detail num">{detail}</pre>
  {/if}
</div>

<style>
  .error {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    padding: var(--space-5);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    border-inline-start: 6px solid var(--sig-down);
    max-inline-size: var(--measure);
  }

  .stamp {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--sig-down);
  }

  .heading {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-1);
    color: var(--c-text-strong);
  }

  .body {
    margin: 0;
    line-height: var(--lh-body);
    color: var(--c-text);
    max-inline-size: 60ch;
  }

  .caveat {
    margin: 0;
    padding: var(--space-3);
    background: var(--sig-down-tint);
    font-size: var(--step--1);
    line-height: var(--lh-body);
    max-inline-size: 60ch;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
    padding-block-start: var(--space-1);
  }

  button,
  .fallback {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    cursor: pointer;
    text-decoration: none;
  }

  .retry {
    background: var(--c-blue);
    color: var(--c-cream);
    border: 0;
  }

  .retry:hover {
    background: var(--c-ink);
  }

  .fallback,
  .toggle {
    background: var(--c-surface);
    box-shadow: var(--ring);
    border: 0;
    color: var(--c-text-accent-strong);
  }

  .toggle {
    text-transform: none;
    letter-spacing: var(--ls-normal);
    font-family: var(--font-body);
    font-weight: var(--fw-medium);
    box-shadow: none;
    text-decoration: underline;
    text-underline-offset: 0.2em;
    padding-inline: var(--space-1);
  }

  .detail {
    margin: 0;
    inline-size: 100%;
    padding: var(--space-3);
    background: var(--c-surface-sunken);
    box-shadow: var(--shadow-inset);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    overflow-x: auto;
    white-space: pre-wrap;
  }
</style>
