<script lang="ts">
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
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
    notAffiliatedSentence,
    operatorLine,
    siteName,
    trademarkSentence
  } from "$lib/legal";
</script>

<svelte:head>
  <title>Disclaimer and non-endorsement · LoL Stats</title>
  <meta name="description" content="The full non-endorsement, trademark and accuracy statement for this unofficial League of Legends statistics site." />
</svelte:head>

<div class="stack stack-7">
  <PageHeader
    eyebrow="Disclaimer"
    title="Disclaimer and non-endorsement"
    lede="The full text every page on this site links to. Nothing here is paraphrased."
  />

  <Callout tone="honesty" label="Non-endorsement">
    <p>{nonEndorsementNotice}</p>
  </Callout>

  <section class="section">
    <SectionHeader title="Independence and trademarks" level={2} />
    <ul class="statements">
      <li>{notAffiliatedSentence}</li>
      <li>{trademarkSentence}</li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="Prices, accounts and ratings" level={2} />
    <ul class="statements">
      <li>{freeAndUngatedSentence}</li>
      <li>{noRatingSentence}</li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="What is published" level={2} />
    <ul class="statements">
      <li>{derivedOnlySentence}</li>
    </ul>
    <Card>
      <h3>In short</h3>
      <p>
        {siteName} shows aggregate statistics computed from recorded match outcomes. It does not show
        players, accounts or match histories, and it does not now or later sell access to the data behind
        its aggregates.
      </p>
    </Card>
  </section>

  <section class="section">
    <SectionHeader title="Who operates this site" level={2} />
    <ul class="statements">
      <li>{operatorLine}</li>
      <li>{contactLine}</li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="Accuracy" level={2} />
    <div class="prose">
      <p>
        Statistics are published as computed, including the samples they rest on and the count of cells that
        were withheld. Even so, they are derived from a partial record of games played and are provided
        without warranty: use them as an indication of what a patch's data looked like, not as a
        guarantee about any particular match, player or rank.
      </p>
      <p>
        Where a figure is stale, the interface says so in words rather than by colour alone, and the build
        it came from is stamped on the page that shows it.
      </p>
    </div>
  </section>

  <Well tone="paper">
    <SectionHeader title="Contact" level={3} />
    <p>
      Corrections, rights enquiries and questions about these pages go to
      <a href={`mailto:${contactEmail}`}>{contactEmail}</a>.
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

  h3 {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-1);
    color: var(--c-text-strong);
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
