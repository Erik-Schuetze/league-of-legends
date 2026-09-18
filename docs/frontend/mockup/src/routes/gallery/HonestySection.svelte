<script lang="ts">
  import Specimen from "./Specimen.svelte";
  import Button from "$lib/components/Button.svelte";
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
  import CoverageStamp from "$lib/components/CoverageStamp.svelte";
  import EmptyState from "$lib/components/EmptyState.svelte";
  import ErrorState from "$lib/components/ErrorState.svelte";
  import NoPublishedData from "$lib/components/NoPublishedData.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import SampleAnnotation from "$lib/components/SampleAnnotation.svelte";
  import Skeleton from "$lib/components/Skeleton.svelte";
  import StalenessIndicator from "$lib/components/StalenessIndicator.svelte";
  import SuppressionSummary from "$lib/components/SuppressionSummary.svelte";

  /* Fixed references so every freshness state can be shown side by side. Pages
     never pass `now`: they let the component read the clock after hydration. */
  const generatedAt = "2026-09-15T04:10:00Z";
  const day = 86_400_000;
  const buildMs = Date.parse(generatedAt);
</script>

<div class="stack stack-8">
  <Specimen
    name="CoverageStamp.svelte"
    state="stamp · card · with source window · with suppression"
    note="Sits directly above every table and on the export header. If a reader can see a number, they can see what it covers."
  >
    <div class="stack stack-6">
      <CoverageStamp
        patch="16.18"
        region="EUW"
        queue="Ranked Solo/Duo"
        bracket="All ranks"
        {generatedAt}
      />
      <div class="grid">
        <CoverageStamp
          patch="16.18"
          region="EUW"
          queue="Ranked Solo/Duo"
          bracket="All ranks"
          {generatedAt}
          sourceWindow={{ from: "2026-09-08", to: "2026-09-14" }}
          minCellN={500}
          suppressedCells={3}
          buildRunId={2}
          variant="card"
        />
        <CoverageStamp
          patch="16.17"
          region="NA"
          queue="Ranked Flex"
          bracket="Diamond+"
          generatedAt="2026-09-08T04:10:00Z"
          sourceWindow={{ from: "2026-09-01", to: "2026-09-07" }}
          minCellN={500}
          variant="card"
        />
      </div>
    </div>
  </Specimen>

  <Specimen
    name="ProvenanceLine.svelte"
    state="demo (loud) · riot (quiet) · block"
    flag="honesty"
    note="A demo build must never be mistakable for real match records. The loud variant is the default state of this mockup."
  >
    <div class="stack stack-4">
      <ProvenanceLine source="demo" detail="fixtures/site/v1 · build run 2 · 0000000000000000000000000000000000000002" />
      <ProvenanceLine source="synthetic" />
      <ProvenanceLine source="riot" detail="Match-V5 ingest, 2026-09-15" />
      <ProvenanceLine source="demo" variant="block" detail="Every figure on this page comes from fixture artifacts committed to the repository. They are shaped like the real thing and are not the real thing." />
    </div>
  </Specimen>

  <Specimen
    name="StalenessIndicator.svelte"
    state="fresh · aging · stale · unknown clock"
    flag="honesty"
    note="Three amounts of urgency, each with a glyph and a word as well as a hue. The age is never computed at build time."
  >
    <div class="stack stack-3">
      <StalenessIndicator {generatedAt} now={buildMs + day} />
      <StalenessIndicator {generatedAt} now={buildMs + 5 * day} />
      <StalenessIndicator {generatedAt} now={buildMs + 21 * day} />
      <StalenessIndicator generatedAt="not-a-date" now={buildMs} />
    </div>
  </Specimen>

  <Specimen
    name="SuppressionSummary.svelte"
    state="full · compact · no suppression"
    flag="honesty"
    note="States the floor, the count and - when the partition has one - the searchable path to a fuller slice. Withheld cells are never shown as zero."
  >
    <div class="stack stack-6">
      <SuppressionSummary suppressedCells={3} minCellN={500} publishedCells={141} unpublishedPairs={24} />
      <SuppressionSummary suppressedCells={3} minCellN={500} publishedCells={141} density="compact" />
      <SuppressionSummary suppressedCells={0} minCellN={500} publishedCells={141} density="compact" />
    </div>
  </Specimen>

  <Specimen
    name="NoPublishedData.svelte"
    state="bracket not published · patch not published · nothing at all"
    flag="honesty"
    note="The opposite of an empty result: the slice was never built. It names the slice in words and offers a working neighbour."
  >
    <div class="stack stack-6">
      <Card>
        <NoPublishedData
          availablePatches={["16.18", "16.17"]}
          requested="16.18"
          region="EUW"
          queue="Ranked Solo/Duo"
          bracket="Diamond+"
          suggestedHref="#gallery"
          suggestedLabel="Show all ranks on patch 16.18"
        />
      </Card>
      <Card>
        <NoPublishedData
          availablePatches={["16.18", "16.17"]}
          requested="16.12"
          region="EUW"
          queue="Ranked Solo/Duo"
        />
      </Card>
      <Card>
        <NoPublishedData availablePatches={[]} />
      </Card>
    </div>
  </Specimen>

  <Specimen
    name="EmptyState.svelte"
    state="table · page · with the unfiltered size"
    note="Filters matched nothing. The unfiltered count proves data exists and the filters hid it, and clearing them is always one action away."
  >
    <div class="stack stack-6">
      <EmptyState
        what="cells"
        activeFilters={[
          { label: "Role", value: "MID" },
          { label: "Min games", value: "5,000" },
          { label: "Tier", value: "S+" }
        ]}
        availableCount={{ rows: 141, noun: "cells" }}
        onclear={() => {}}
      />
      <EmptyState
        what="published cells"
        variant="page"
        activeFilters={[{ label: "Region", value: "OCE" }]}
        onclear={() => {}}
      />
    </div>
  </Specimen>

  <Specimen
    name="ErrorState.svelte"
    state="with retry · with detail · stale data · no retry"
    flag="honesty"
    note="Says what happened, what it means for the numbers on screen, and what is safe to assume. A stale view keeps its data visible and is marked."
  >
    <div class="stack stack-6">
      <Card>
        <ErrorState
          title="The filter change could not be applied"
          body="The explorer kept the previous view. The figures on screen are still the last published build, so nothing here is wrong: it is simply older than the patch you asked for."
          detail="TypeError: cannot read properties of undefined (reading 'cells') at applyFilter (explore.js:214)"
          onretry={() => {}}
          fallbackHref="#gallery"
          fallbackLabel="Show patch 16.18, all ranks"
        />
      </Card>
      <Card>
        <ErrorState
          body="No build is published for this slice and the last good revision is more than a week old. Treat the figures below as historical."
          staleData
        />
      </Card>
    </div>
  </Specimen>

  <Specimen name="Skeleton.svelte" state="rows · card · text">
    <div class="grid">
      <Skeleton label="Published champion-role cells" shape="rows" rows={5} />
      <Skeleton label="Median win rate" shape="card" />
      <Skeleton label="Method notes" shape="text" />
    </div>
  </Specimen>

  <Specimen
    name="Callout.svelte + SampleAnnotation.svelte"
    state="the honesty family working together in prose"
    note="This is the composition the guide asks for wherever a figure is introduced in running text."
  >
    <div class="stack stack-4">
      <Callout tone="honesty" label="What this slice does not cover">
        <p>
          Three champion-role cells are withheld because they fall below the 500 game floor. They are not
          zero and they are not the bottom of the table: they are unknown.
        </p>
        <p class="sample-line"><SampleAnnotation n={12480} minCellN={500} /></p>
      </Callout>
      <div class="cluster">
        <Button variant="ghost" size="sm">Show withheld cells</Button>
        <Button variant="ghost" size="sm">Read the method note</Button>
      </div>
    </div>
  </Specimen>
</div>

<style>
  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr));
  }

  .sample-line {
    margin: 0;
  }
</style>
