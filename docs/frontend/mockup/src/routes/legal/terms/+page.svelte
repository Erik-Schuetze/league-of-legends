<script lang="ts">
  import Callout from "$lib/components/Callout.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import Well from "$lib/components/Well.svelte";
  import {
    contactEmail,
    contactLine,
    derivedOnlySentence,
    effectiveLine,
    freeAndUngatedSentence,
    noRatingSentence,
    nonEndorsementNotice,
    operatorLine,
    siteName
  } from "$lib/legal";
</script>

<svelte:head>
  <title>Terms of service · LoL Stats</title>
  <meta name="description" content="Terms for using this site and for reusing the aggregate statistics it publishes." />
</svelte:head>

<div class="stack stack-7">
  <PageHeader
    eyebrow="Legal"
    title="Terms of service"
    lede="Terms for using this site and for reusing what it publishes."
  />

  <section class="section">
    <SectionHeader title="Using the site" level={2} />
    <ul class="statements">
      <li>{freeAndUngatedSentence}</li>
      <li>
        You may read, quote and reuse the published aggregate statistics, with attribution to {siteName} and
        a note of the patch and partition they came from, because that attribution is what keeps an
        aggregate meaningful once it is separated from the page that computed it.
      </li>
      <li>
        You may not present the site or its output as endorsed by, affiliated with or approved by Riot Games,
        or as an official source of League of Legends statistics.
      </li>
      <li>
        Automated access is not offered, and no rate-limited or gated tier is planned. If you need a bulk
        extract, ask.
      </li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="What is not offered" level={2} />
    <ul class="statements">
      <li>{noRatingSentence}</li>
      <li>{derivedOnlySentence}</li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="No warranty" level={2} />
    <div class="prose">
      <p>
        The site and its statistics are provided as is, without warranty of any kind. Derived statistics rest
        on a partial record of games; they are published with their sample sizes and intervals so their
        limits are visible, but they are not a guarantee about any match, player or rank.
      </p>
      <p>
        The operator is not liable for decisions taken on the basis of these statistics, nor for any
        interruption of service. Where a build is stale or a slice is missing, the interface states it
        rather than filling the gap.
      </p>
    </div>
  </section>

  <Callout tone="honesty" label="Trademarks">
    <p>{nonEndorsementNotice}</p>
  </Callout>

  <section class="section">
    <SectionHeader title="Who is agreeing" level={2} />
    <ul class="statements">
      <li>{operatorLine}</li>
    </ul>
  </section>

  <Well tone="paper">
    <SectionHeader title="Contact" level={3} />
    <p>{contactLine}</p>
    <p>
      <a href={`mailto:${contactEmail}`}>{contactEmail}</a>
    </p>
    <p class="fine num">{effectiveLine}</p>
  </Well>
</div>

<style>
  .section {
    display: grid;
    gap: var(--space-4);
  }

  .statements {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--space-4);
  }

  .statements li {
    max-inline-size: var(--measure);
    line-height: var(--lh-body);
    padding-inline-start: var(--space-4);
    border-inline-start: 2px solid var(--c-rule);
  }

  .prose p {
    margin: 0;
    max-inline-size: var(--measure);
    line-height: var(--lh-body);
  }

  .prose p + p {
    margin-block-start: var(--space-4);
  }

  p {
    margin: 0;
    line-height: var(--lh-body);
  }

  .fine {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  a {
    color: var(--c-text-accent);
  }
</style>
