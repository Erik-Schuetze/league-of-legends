<script lang="ts">
  import Button from "$lib/components/Button.svelte";
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
  import Chip from "$lib/components/Chip.svelte";
  import CoverageStamp from "$lib/components/CoverageStamp.svelte";
  import FloatingTile from "$lib/components/FloatingTile.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import StatTile from "$lib/components/StatTile.svelte";
  import Well from "$lib/components/Well.svelte";
  import {
    cells,
    coverage,
    partitions,
    patchesAvailable,
    roles,
    tiers
  } from "$lib/data/demo.generated";
  import {
    freeAndUngatedSentence,
    noRatingSentence,
    notAffiliatedSentence,
    siteName
  } from "$lib/legal";

  const queueLabels: Record<number, string> = {
    400: "Normal Draft",
    420: "Ranked Solo/Duo",
    440: "Ranked Flex"
  };

  const partitionWindow: Record<string, { generated: string; from: string; to: string }> = {
    "16.18": { generated: "2026-09-15T04:10:00Z", from: "2026-09-08", to: "2026-09-14" },
    "16.17": { generated: "2026-09-08T04:10:00Z", from: "2026-09-01", to: "2026-09-07" }
  };
</script>

<svelte:head>
  <title>League of Legends statistics · LoL Stats</title>
  <meta name="description" content="Derived League of Legends statistics with the sample behind every number: win rates, pick rates and role breakdowns for published champion-role cells, patch by patch." />
</svelte:head>

<div class="stack stack-8">
  <section class="hero" aria-labelledby="hero-title">
    <div class="hero-copy">
      <p class="eyebrow">Patch {coverage.patch} · {coverage.region}</p>
      <h1 id="hero-title">League of Legends statistics, with the sample behind every number.</h1>
      <p class="lede">
        Win rates, pick rates and role breakdowns for published champion-role cells, each one carrying the
        games it was computed from, the window those games came from, and an honest mark wherever the sample
        was too small to publish.
      </p>
      <div class="cluster cluster-4">
        <Button variant="primary" href="#entry">Explore the data</Button>
        <Button variant="secondary" href="#coverage">What is published</Button>
        <Button variant="ghost" href="/gallery">Component gallery</Button>
      </div>
    </div>

    <div class="tiles" aria-hidden="true">
      <FloatingTile float phase="a" label="Published cells">{cells.length}</FloatingTile>
      <FloatingTile float phase="b" label="Patches">{patchesAvailable.length}</FloatingTile>
      <FloatingTile float phase="c" label="Roles">{roles.length}</FloatingTile>
      <FloatingTile label="Minimum sample">{coverage.min_cell_n}</FloatingTile>
    </div>
  </section>

  <section class="block" id="entry" aria-labelledby="entry-title">
    <SectionHeader title="Start with the explorer" id="entry-title">
      {#snippet aside()}
        <a class="aside-link" href="/explore">/explore</a>
      {/snippet}
    </SectionHeader>
    <div class="grid">
      <Card>
        <p class="eyebrow">First slice</p>
        <h3>A data explorer</h3>
        <p>
          One table over every published champion-role cell, with the region, queue, bracket, patch range and
          role filters needed to narrow it. Sort by any column, export what you see, and share the view as a
          link.
        </p>
        <div class="cluster">
          <Button variant="secondary" href="/explore">Open the explorer</Button>
        </div>
      </Card>
      <Card>
        <p class="eyebrow">Honesty</p>
        <h3>Nothing is quietly rounded away</h3>
        <p>
          Cells below the publication floor are withheld and labelled, never shown as zero. Every table
          carries its coverage stamp, and a stale build says so in words.
        </p>
        <div class="cluster cluster-2">
          <Chip label="Withheld" tone="unknown" value={String(coverage.suppressed_cells)} />
          <Chip label="Floor" value={coverage.min_cell_n.toLocaleString("en-GB")} />
        </div>
      </Card>
      <Card>
        <p class="eyebrow">Portable</p>
        <h3>Exports that explain themselves</h3>
        <p>
          CSV and JSON carry the patch, region, queue, bracket, source window and minimum sample in the file
          header, so a spreadsheet is still readable after it leaves the site.
        </p>
      </Card>
    </div>
  </section>

  <section class="block" id="coverage" aria-labelledby="coverage-title">
    <SectionHeader title="What is published" id="coverage-title">
      {#snippet aside()}
        <span class="num">{partitions.length} partitions</span>
      {/snippet}
    </SectionHeader>
    <div class="grid">
      {#each partitions as partition (partition.patch)}
        <Card>
          <CoverageStamp
            patch={partition.patch}
            region={partition.region}
            queue={queueLabels[partition.queue] ?? String(partition.queue)}
            bracket={partition.bracket === "all" ? "All ranks" : partition.bracket}
            generatedAt={partitionWindow[partition.patch]?.generated ?? coverage.generated_at}
            sourceWindow={{
              from: partitionWindow[partition.patch]?.from ?? coverage.source_window.from,
              to: partitionWindow[partition.patch]?.to ?? coverage.source_window.to
            }}
            minCellN={coverage.min_cell_n}
            suppressedCells={partition.suppressed_cells}
            variant="card"
          />
          <div class="partition-stats">
            <StatTile
              label="Cells"
              value={String(partition.cells_published)}
              note="Champion-role pairs above the floor."
            />
            <StatTile
              label="Withheld"
              value={String(partition.suppressed_cells)}
              note="Below the floor, so not published."
            />
          </div>
          <div class="cluster">
            <Button variant="ghost" size="sm" href="/explore">Explore {partition.patch}</Button>
          </div>
        </Card>
      {/each}
    </div>
    <Well tone="paper">
      <p class="eyebrow">Not published yet</p>
      <p class="muted">
        Every other region, queue and bracket combination is absent rather than empty. Asking for one in the
        explorer answers with the slice's name in words and a link to a slice that exists, never with a table
        of zeroes.
      </p>
    </Well>
  </section>

  <section class="block" aria-labelledby="promise-title">
    <SectionHeader title="What we promise, in writing" id="promise-title" />
    <div class="grid">
      <Card>
        <h3>Free and ungated</h3>
        <p>{freeAndUngatedSentence}</p>
      </Card>
      <Card>
        <h3>No rating, ever</h3>
        <p>{noRatingSentence}</p>
      </Card>
      <Card>
        <h3>Independent</h3>
        <p>{notAffiliatedSentence}</p>
      </Card>
    </div>
    <Callout tone="honesty" label="Tiers are ranks, not skill">
      <p>
        A tier badge ranks a champion inside one partition. It is not a claim about the player base, and it
        is not comparable across patches. The explorer always states the partition in the same breath.
      </p>
    </Callout>
  </section>

  <section class="block" aria-labelledby="toolbox-title">
    <SectionHeader title="The toolbox behind this page" id="toolbox-title">
      {#snippet aside()}
        <span class="num">51 components</span>
      {/snippet}
    </SectionHeader>
    <p class="lede">
      {siteName} is built from a documented set of components - surfaces, controls, tables and the honesty
      family - with every state drawn out in the gallery, so implementation agents work from evidence rather
      than from memory.
    </p>
    <div class="cluster cluster-2">
      {#each tiers as tier (tier)}
        <Chip label={tier} tone="accent" />
      {/each}
      {#each roles as role (role)}
        <Chip label={role} />
      {/each}
    </div>
    <div class="cluster">
      <Button variant="secondary" href="/gallery">Open the gallery</Button>
      <Button variant="ghost" href="/about">About the data</Button>
    </div>
  </section>
</div>

<style>
  .hero {
    display: grid;
    gap: var(--space-7);
    align-items: center;
    padding-block: var(--space-6) var(--space-5);
  }

  @media (min-width: 1024px) {
    .hero {
      grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
      gap: var(--space-8);
    }
  }

  .hero-copy {
    display: grid;
    gap: var(--space-4);
  }

  h1 {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-5);
    line-height: var(--lh-tight);
    letter-spacing: var(--ls-tight);
    color: var(--c-text-strong);
    max-inline-size: 24ch;
  }

  .tiles {
    display: grid;
    gap: var(--space-6);
    grid-template-columns: repeat(2, minmax(0, 1fr));
    justify-items: center;
  }

  @media (min-width: 768px) {
    .tiles {
      grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    }
  }

  .block {
    display: grid;
    gap: var(--space-5);
  }

  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(19rem, 1fr));
  }

  h3 {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-2);
    line-height: var(--lh-tight);
    color: var(--c-text-strong);
  }

  p {
    margin: 0;
    line-height: var(--lh-body);
    max-inline-size: var(--measure);
  }

  .muted {
    color: var(--c-text-muted);
  }

  .aside-link {
    color: var(--c-text-accent);
  }

  .partition-stats {
    display: grid;
    gap: var(--space-4);
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
  }
</style>
