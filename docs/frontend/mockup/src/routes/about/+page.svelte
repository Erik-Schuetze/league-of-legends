<script lang="ts">
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
  import CoverageStamp from "$lib/components/CoverageStamp.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import SuppressionSummary from "$lib/components/SuppressionSummary.svelte";
  import Well from "$lib/components/Well.svelte";
  import { cells, coverage, dataSource, partitions, unpublishedPairs } from "$lib/data/demo.generated";
  import { contactEmail, derivedOnlySentence, nonEndorsementNotice, siteName } from "$lib/legal";
</script>

<svelte:head>
  <title>About and data sources · LoL Stats</title>
  <meta name="description" content="How the statistics are collected, aggregated, floor-checked and published, and what the numbers deliberately do not say." />
</svelte:head>

<div class="stack stack-7">
  <PageHeader
    eyebrow="About"
    title="Where these numbers come from"
    lede="What is aggregated, what is deliberately left out, and how to tell which build you are looking at."
  >
    {#snippet meta()}
      <CoverageStamp
        patch={coverage.patch}
        region={coverage.region}
        queue={String(coverage.queue)}
        bracket={coverage.bracket}
        generatedAt={coverage.generated_at}
        sourceWindow={coverage.source_window}
        minCellN={coverage.min_cell_n}
        suppressedCells={coverage.suppressed_cells}
        buildRunId={coverage.build_run_id}
      />
    {/snippet}
  </PageHeader>

  <ProvenanceLine
    source={dataSource}
    variant="block"
    detail="This mockup renders committed fixture artifacts from fixtures/site/v1. The shape matches the production contract; the values do not describe real matches."
  />

  <section class="section prose">
    <SectionHeader title="What the site publishes" level={2} />
    <p>
      {siteName} publishes derived aggregate statistics for League of Legends: per-patch, per-region,
      per-queue and per-bracket champion-role cells with a win rate, a pick rate, a ban rate, a tier rank
      and a confidence interval over the same games.
    </p>
    <p>{derivedOnlySentence}</p>
    <p>
      Every published figure carries its sample. A cell needs at least
      {coverage.min_cell_n.toLocaleString("en-GB")} games in the window before it is published at all, and a cell that
      does not clear that floor is withheld rather than estimated.
    </p>
  </section>

  <section class="section">
    <SectionHeader title="How a build is assembled" level={2} />
    <ol class="steps">
      <li>
        <h3>Collect</h3>
        <p>Completed matches are read from the Riot match endpoints for the patch window being built.</p>
      </li>
      <li>
        <h3>Aggregate</h3>
        <p>
          Counts are accumulated per champion and role. No per-player record survives this step: the
          aggregate is the only artifact that leaves it.
        </p>
      </li>
      <li>
        <h3>Apply the floor</h3>
        <p>
          Cells below the minimum sample are withheld and counted. The count travels with the build, so the
          interface can say how many were withheld instead of implying none were.
        </p>
      </li>
      <li>
        <h3>Publish with provenance</h3>
        <p>
          Each partition is stamped with the patch, region, queue, bracket, source window, minimum sample,
          withholding count and build identity. The interface renders that stamp wherever a number appears.
        </p>
      </li>
    </ol>
  </section>

  <section class="section">
    <SectionHeader title="What is published right now" level={2}>
      {#snippet aside()}
        <span class="num">{partitions.length} partitions</span>
      {/snippet}
    </SectionHeader>
    <div class="grid">
      {#each partitions as partition (partition.patch)}
        <Card>
          <h3 class="num">
            Patch {partition.patch} · {partition.region} · queue {partition.queue} · {partition.bracket}
          </h3>
          <p class="num">
            {partition.cells_published} cells published, {partition.suppressed_cells} withheld.
          </p>
        </Card>
      {/each}
    </div>
    <SuppressionSummary
      suppressedCells={coverage.suppressed_cells}
      minCellN={coverage.min_cell_n}
      publishedCells={cells.length}
      unpublishedPairs={unpublishedPairs.length}
    />
  </section>

  <section class="section prose">
    <SectionHeader title="What the numbers do not say" level={2} />
    <ul>
      <li>
        <strong>Tiers are ranks inside one partition.</strong> A tier is assigned by comparing cells in the
        same patch, region, queue and bracket. It is not comparable across patches and says nothing about
        individual players.
      </li>
      <li>
        <strong>A wide interval is a small sample.</strong> The 95% interval half-width is printed next to
        the rate precisely so a narrow lead is not mistaken for a fact.
      </li>
      <li>
        <strong>Withheld is not zero.</strong> A withheld cell is unknown. It is never sorted to the bottom
        of a table as if it were the worst performer.
      </li>
      <li>
        <strong>A pick rate is a share of games, not of players.</strong> It counts how often a champion was
        picked, not how many people picked it.
      </li>
    </ul>
  </section>

  <Callout tone="honesty" label="Trademark and non-endorsement">
    <p>{nonEndorsementNotice}</p>
  </Callout>

  <Well tone="paper">
    <SectionHeader title="Contact" level={3} />
    <p>
      Questions about the data, corrections or rights enquiries go to
      <a href={`mailto:${contactEmail}`}>{contactEmail}</a>.
    </p>
  </Well>
</div>

<style>
  .section {
    display: grid;
    gap: var(--space-4);
  }

  .prose p,
  .prose li {
    max-inline-size: var(--measure);
    line-height: var(--lh-body);
  }

  .prose ul {
    margin: 0;
    padding-inline-start: var(--space-5);
    display: grid;
    gap: var(--space-3);
  }

  .steps {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
    counter-reset: step;
  }

  .steps li {
    counter-increment: step;
    display: grid;
    gap: var(--space-2);
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }

  .steps li::before {
    content: counter(step);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    color: var(--c-text-accent-strong);
  }

  h3 {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-1);
    line-height: var(--lh-tight);
    color: var(--c-text-strong);
  }

  .steps p {
    margin: 0;
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr));
  }

  .grid h3 {
    font-size: var(--step-0);
  }

  .grid p {
    margin: 0;
  }

  a {
    color: var(--c-text-accent);
  }
</style>
