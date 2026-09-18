<script lang="ts">
  import PageHeader from "$lib/components/PageHeader.svelte";
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
  import CoverageStamp from "$lib/components/CoverageStamp.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import Well from "$lib/components/Well.svelte";
  import { coverage, generatedAt } from "$lib/data/demo.generated";

  import ControlsSection from "./ControlsSection.svelte";
  import DataDisplaySection from "./DataDisplaySection.svelte";
  import FoundationsSection from "./FoundationsSection.svelte";
  import HonestySection from "./HonestySection.svelte";
  import Specimen from "./Specimen.svelte";
  import SurfacesSection from "./SurfacesSection.svelte";

  const contents = [
    { href: "#foundations", label: "Foundations", note: "colour, type, elevation, spacing, motion, focus" },
    { href: "#surfaces", label: "Surfaces and overlays", note: "23 components" },
    { href: "#controls", label: "Controls", note: "18 components" },
    { href: "#data", label: "Data display", note: "10 components" },
    { href: "#honesty", label: "Data honesty", note: "8 components, every state" },
    { href: "#layout", label: "Layout and composition", note: "shell, measure, rail, tables in cards" },
    { href: "#states", label: "The state matrix", note: "what each data component must render" }
  ];

  const stateMatrix: { component: string; states: string }[] = [
    { component: "DataTable", states: "ready · loading (skeleton rows) · empty (filters) · no published data · error · stale · narrow (horizontal scroll with a frozen first column)" },
    { component: "StatTile", states: "value · loading · withheld · delta up/down/flat · linked" },
    { component: "BarMeter", states: "value · reference line · withheld · small" },
    { component: "HeatmapMatrix", states: "published · withheld (hatched) · absent (dash) · narrow (columns scroll, row labels frozen)" },
    { component: "CoverageStamp", states: "stamp · card · with suppression · with build provenance" },
    { component: "StalenessIndicator", states: "fresh · aging · stale · clock unknown" },
    { component: "SuppressionSummary", states: "full · compact · nothing suppressed" },
    { component: "NoPublishedData", states: "requested slice missing · nothing published at all" },
    { component: "EmptyState", states: "table · page · with the unfiltered count" },
    { component: "ErrorState", states: "with retry · with technical detail · stale data retained · no retry offered" }
  ];
</script>

<svelte:head>
  <title>Component gallery · LoL Stats</title>
  <meta name="description" content="Every element of the design system in every state it must render: foundations, surfaces, controls, data display and the data-honesty components." />
</svelte:head>

<!-- The demo links inside specimens point at #gallery: a link in a specimen needs
     somewhere real to go, and a dead href is a bad example to copy. -->
<div class="stack stack-8" id="gallery">
  <PageHeader
    eyebrow="Design guide"
    title="Component gallery"
    lede="Every element in the toolbox, in every state it must be able to render. This page is the review surface and the verification corpus: the assertions in tools/check-mockup.mjs run against it."
  >
    {#snippet meta()}
      <p class="num">27px grid · 2px ring · 8px card offset · 12px hero offset · zero blur · zero radius</p>
    {/snippet}
  </PageHeader>

  <Callout tone="honesty" label="How to read this page">
    <p>
      Each block names the component file it documents, then the states shown. Data in the specimens is a
      mix of the committed demo artifacts and hand-written rows chosen to show a state that the current
      partition does not contain. Nothing here is loaded from the network, and nothing here is real match
      data.
    </p>
  </Callout>

  <div class="contents-grid">
    <Well tone="paper">
      <nav aria-labelledby="gallery-contents">
        <p class="eyebrow" id="gallery-contents">On this page</p>
        <ol class="contents link-list">
          {#each contents as item (item.href)}
            <li>
              <a href={item.href}>{item.label}</a>
              <span class="note">{item.note}</span>
            </li>
          {/each}
        </ol>
      </nav>
    </Well>
    <Card>
      <SectionHeader title="This page's coverage" level={3} />
      <CoverageStamp
        patch={coverage.patch}
        region={coverage.region}
        queue="Ranked Solo/Duo"
        bracket="All ranks"
        {generatedAt}
        sourceWindow={coverage.source_window}
        minCellN={coverage.min_cell_n}
        suppressedCells={coverage.suppressed_cells}
        buildRunId={coverage.build_run_id}
        variant="card"
      />
      <ProvenanceLine source={coverage.source} detail="fixtures/site/v1" />
    </Card>
  </div>

  <section id="foundations" class="block" aria-labelledby="foundations-title">
    <SectionHeader title="Foundations" id="foundations-title">
      {#snippet aside()}
        <span class="num">tokens.css</span>
      {/snippet}
    </SectionHeader>
    <FoundationsSection />
  </section>

  <section id="surfaces" class="block" aria-labelledby="surfaces-title">
    <SectionHeader title="Surfaces and overlays" id="surfaces-title">
      {#snippet aside()}
        <span class="num">23 components</span>
      {/snippet}
    </SectionHeader>
    <SurfacesSection />
  </section>

  <section id="controls" class="block" aria-labelledby="controls-title">
    <SectionHeader title="Controls" id="controls-title">
      {#snippet aside()}
        <span class="num">18 components</span>
      {/snippet}
    </SectionHeader>
    <ControlsSection />
  </section>

  <section id="data" class="block" aria-labelledby="data-title">
    <SectionHeader title="Data display" id="data-title">
      {#snippet aside()}
        <span class="num">10 components</span>
      {/snippet}
    </SectionHeader>
    <DataDisplaySection />
  </section>

  <section id="honesty" class="block" aria-labelledby="honesty-title">
    <SectionHeader title="Data honesty" id="honesty-title">
      {#snippet aside()}
        <span class="num">8 components</span>
      {/snippet}
    </SectionHeader>
    <HonestySection />
  </section>

  <section id="layout" class="block" aria-labelledby="layout-title">
    <SectionHeader title="Layout and composition" id="layout-title" />

    <Specimen name="SiteNav.svelte · SiteFooter.svelte" state="rendered by the layout on every route">
      <p class="use">
        Both are visible on this page: the nav at the top with its gradient fade and the current page
        marked, the footer at the bottom with the legal lines and the build stamp. They are layout
        components and take no props from a page.
      </p>
    </Specimen>

    <Specimen name="app.css" state=".shell · .measure · .stack · .cluster · .grid-auto">
      <div class="utilities">
        <div class="utility">
          <p class="eyebrow">.shell</p>
          <p class="use">Maximum width <code>--shell-max</code> (1400px) with <code>--gutter</code> padding. Every page is one shell.</p>
        </div>
        <div class="utility">
          <p class="eyebrow">.measure</p>
          <p class="use">Prose column, 56rem. Anything longer than three lines should be inside one.</p>
        </div>
        <div class="utility">
          <p class="eyebrow">.stack / .cluster / .grid-auto</p>
          <p class="use">
            Vertical rhythm, wrapping rows of controls, and self-fitting grids. Pages compose these instead of
            writing new layout CSS.
          </p>
        </div>
      </div>
    </Specimen>

    <Specimen name="composition" state="split layout with a filter rail, desktop first">
      <div class="split">
        <aside class="rail" aria-label="Example filter rail">
          <Well tone="paper">
            <p class="eyebrow">Filters</p>
            <p class="use">A 16rem rail above 1024px. Below that the rail's contents move into a drawer.</p>
          </Well>
        </aside>
        <div class="main">
          <Card flush>
            <div class="table-stub">
              <p class="num">The table takes the remaining width and scrolls horizontally before it wraps.</p>
            </div>
          </Card>
        </div>
      </div>
    </Specimen>

    <Specimen name="composition" state="in-page contents built from primitives">
      <p class="use">
        There is no contents component: it is a <code>Well</code>, an <code>.eyebrow</code> and an ordered
        list, as used at the top of this page. A page with fewer than four sections does not need one.
      </p>
    </Specimen>
  </section>

  <section id="states" class="block" aria-labelledby="states-title">
    <SectionHeader title="The state matrix" id="states-title">
      {#snippet aside()}
        <a href="#states-title">components.md</a>
      {/snippet}
    </SectionHeader>
    <Card flush>
      <div class="matrix-wrap">
        <table class="matrix">
          <caption>Required states for every data component</caption>
          <thead>
            <tr><th scope="col">Component</th><th scope="col">States that must render</th></tr>
          </thead>
          <tbody>
            {#each stateMatrix as row (row.component)}
              <tr>
                <th scope="row" class="num">{row.component}</th>
                <td>{row.states}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </Card>
  </section>
</div>

<style>
  .contents-grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .contents-grid {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
    }
  }

  .contents {
    margin: var(--space-3) 0 0;
    padding-inline-start: var(--space-5);
    display: grid;
    gap: var(--space-2);
  }

  .contents li {
    line-height: var(--lh-body);
  }

  .contents a {
    color: var(--c-text-accent);
  }

  .note {
    display: block;
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }

  .block {
    display: grid;
    gap: var(--space-6);
  }

  .utilities {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr));
  }

  .utility {
    display: grid;
    gap: var(--space-2);
    align-content: start;
  }

  .split {
    display: grid;
    gap: var(--space-5);
  }

  @media (min-width: 1024px) {
    .split {
      grid-template-columns: var(--rail-width) minmax(0, 1fr);
      align-items: start;
    }
  }

  .rail,
  .main {
    min-inline-size: 0;
  }

  .table-stub {
    padding: var(--space-5);
  }

  .use {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
    max-inline-size: 76ch;
    line-height: var(--lh-body);
  }

  .use code {
    font-family: var(--font-mono);
    background: var(--tint-accent);
    padding: 0 var(--space-1);
  }

  .matrix-wrap {
    overflow-x: auto;
  }

  .matrix {
    inline-size: 100%;
    border-collapse: collapse;
    font-size: var(--step--1);
  }

  .matrix caption {
    text-align: start;
    padding: var(--space-3) var(--space-4);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .matrix th,
  .matrix td {
    text-align: start;
    vertical-align: top;
    padding: var(--space-3) var(--space-4);
    border-block-end: 1px solid var(--c-rule);
    line-height: var(--lh-body);
  }

  .matrix thead th {
    background: var(--c-surface-sunken);
    border-block-end: 2px solid var(--c-rule-strong);
    font-size: var(--step--2);
    text-transform: uppercase;
    letter-spacing: var(--ls-wide);
    white-space: nowrap;
  }

  .matrix tbody th {
    color: var(--c-text-accent-strong);
    white-space: nowrap;
  }
</style>
