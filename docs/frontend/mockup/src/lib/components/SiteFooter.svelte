<script lang="ts">
  import {
    contactEmail,
    derivedOnlySentence,
    noRatingSentence,
    nonEndorsementNotice,
    notAffiliatedSentence,
    siteName,
    trademarkSentence,
  } from "$lib/legal";

  interface Props {
    /** Second line of the build stamp: patch, region, generation time. */
    buildStamp?: string;
  }

  let { buildStamp = undefined }: Props = $props();

  const year = 2026;
</script>

<footer class="footer">
  <div class="shell stack stack-6">
    <div class="columns">
      <div class="stack stack-4">
        <p class="wordmark">{siteName}</p>
        <p class="purpose">
          Derived aggregate statistics for League of Legends. No accounts, no
          ratings, no per-player records.
        </p>
        <p class="stamp mono">
          <span>Contact</span>
          <a href={`mailto:${contactEmail}`}>{contactEmail}</a>
        </p>
        {#if buildStamp}
          <p class="stamp mono">{buildStamp}</p>
        {/if}
      </div>

      <nav class="stack stack-4" aria-label="Legal">
        <h2 class="heading">Legal</h2>
        <ul class="stack stack-2">
          <li><a href="/disclaimer">Disclaimer and non-endorsement</a></li>
          <li><a href="/legal/terms">Terms of service</a></li>
          <li><a href="/legal/privacy">Privacy</a></li>
          <li><a href="/about">About and data sources</a></li>
        </ul>
      </nav>

      <nav class="stack stack-4" aria-label="Product">
        <h2 class="heading">Product</h2>
        <ul class="stack stack-2">
          <li><a href="/explore">Data explorer</a></li>
          <li><a href="/gallery">Component gallery</a></li>
          <li><a href="/explore">Champion tiers</a></li>
          <li><a href="/explore">Matchups</a></li>
        </ul>
      </nav>
    </div>

    <!-- Approved wording only. Each sentence is a constant from
         docs/compliance.md and is rendered verbatim. -->
    <div class="legal stack stack-4">
      <p class="notice">{nonEndorsementNotice}</p>
      <p>
        <a href="/disclaimer">Read the full disclaimer</a>
      </p>
      <p>{notAffiliatedSentence}</p>
      <p>{trademarkSentence}</p>
      <p>{derivedOnlySentence}</p>
      <p>{noRatingSentence}</p>
    </div>

    <p class="fine mono">© {year} Erik Schuetze · built as a static build, no third-party scripts</p>
  </div>
</footer>

<style>
  .footer {
    margin-block-start: var(--space-9);
    padding-block: var(--space-7) var(--space-6);
    border-block-start: 2px solid var(--c-rule-strong);
    background: var(--c-surface);
  }

  .columns {
    display: grid;
    gap: var(--space-6);
    grid-template-columns: minmax(16rem, 2fr) repeat(2, minmax(10rem, 1fr));
  }

  .wordmark {
    font-family: var(--font-brand);
    font-weight: var(--fw-bold);
    font-size: var(--step-2);
    color: var(--c-text-strong);
  }

  .purpose {
    max-width: var(--measure-narrow);
    color: var(--c-text-muted);
    font-size: var(--step--1);
  }

  .heading {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  ul a {
    font-size: var(--step--1);
  }

  .stamp {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .legal {
    border-block-start: 2px solid var(--c-rule);
    padding-block-start: var(--space-5);
    max-width: var(--measure);
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }

  .notice {
    font-weight: var(--fw-semibold);
    color: var(--c-text-strong);
  }

  .fine {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  @media (max-width: 767px) {
    .columns {
      grid-template-columns: 1fr;
    }

    /* Footer links are targets too: laid out at body size they are 16px tall,
       which no thumb can hit. The row grows, the type does not. */
    ul a,
    .legal a {
      display: inline-flex;
      align-items: center;
      min-block-size: var(--touch-target);
    }
  }
</style>
