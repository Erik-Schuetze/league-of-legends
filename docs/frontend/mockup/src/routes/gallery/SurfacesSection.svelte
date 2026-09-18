<script lang="ts">
  import Specimen from "./Specimen.svelte";
  import Banner from "$lib/components/Banner.svelte";
  import Breadcrumbs from "$lib/components/Breadcrumbs.svelte";
  import Button from "$lib/components/Button.svelte";
  import Callout from "$lib/components/Callout.svelte";
  import Card from "$lib/components/Card.svelte";
  import Disclosure from "$lib/components/Disclosure.svelte";
  import Dialog from "$lib/components/Dialog.svelte";
  import Drawer from "$lib/components/Drawer.svelte";
  import FloatingTile from "$lib/components/FloatingTile.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import Pagination from "$lib/components/Pagination.svelte";
  import Popover from "$lib/components/Popover.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import Tooltip from "$lib/components/Tooltip.svelte";
  import Well from "$lib/components/Well.svelte";

  let dialogOpen = $state(false);
  let drawerEnd = $state(false);
  let drawerStart = $state(false);
  let tab = $state("summary");
  let page = $state(3);
  let bannerVisible = $state(true);
  let toasts = $state([
    {
      id: "t1",
      tone: "success" as const,
      message: "Filters copied to the address bar.",
      action: { label: "Copy link", onclick: () => navigator.clipboard?.writeText(location.href) }
    },
    { id: "t2", tone: "neutral" as const, message: "Export prepared: 141 rows, 8 columns." },
    { id: "t3", tone: "warning" as const, message: "Patch 16.17 is stale: published 3 days ago." }
  ]);

  const crumbs = [
    { label: "LoL Stats", href: "/" },
    { label: "Explorer", href: "/explore/" },
    { label: "Patch 16.18 · EUW · Ranked Solo" }
  ];

  const tabItems = [
    { id: "summary", label: "Summary", count: 141 },
    { id: "matchups", label: "Matchups", count: 5 },
    { id: "items", label: "Items & runes" }
  ];
</script>

<div class="stack stack-8">
  <Specimen name="Card.svelte" state="default · flush · interactive">
    <div class="grid">
      <Card>
        <SectionHeader title="Patch 16.18 window" level={3} />
        <p>Published 2026-09-04, sourced from ranked solo games on EUW above 500 games per cell.</p>
      </Card>
      <Card flush>
        <div class="flush-body">
          <p class="eyebrow">Flush</p>
          <p>Padding is removed so a table can sit edge to edge. The card still owns the ring and the shadow.</p>
        </div>
      </Card>
      <div class="stretch-host">
        <Card interactive>
          <SectionHeader title="Aatrox · TOP" level={3} />
          <p class="num">53.1% win · n = 12,480</p>
          <a class="stretch-link" href="#gallery">Open the champion page</a>
        </Card>
      </div>
    </div>
  </Specimen>

  <Specimen
    name="FloatingTile.svelte"
    state="static · float a/b/c · sunken · 220px"
    note="Decorative only. A tile never hosts a control: the drift would move the target under the pointer."
  >
    <div class="tiles">
      <FloatingTile label="Static 150">141 cells</FloatingTile>
      <FloatingTile float phase="a" label="Float a">Patch 16.18</FloatingTile>
      <FloatingTile float phase="b" label="Float b">6 regions</FloatingTile>
      <FloatingTile float phase="c" label="Float c">5 roles</FloatingTile>
      <FloatingTile size={220} float phase="b" label="Larger tile">27px grid</FloatingTile>
      <FloatingTile sunken label="Sunken tile">No published data</FloatingTile>
    </div>
  </Specimen>

  <Specimen name="Well.svelte" state="sunken · paper">
    <div class="grid">
      <Well>
        <p class="eyebrow">Sunken well</p>
        <p>Fenced data, keys and legends. Use for anything quoted rather than authored.</p>
      </Well>
      <Well tone="paper">
        <p class="eyebrow">Paper well</p>
        <p>A cream well inside a cream card, separated by the inset rule only.</p>
      </Well>
    </div>
  </Specimen>

  <Specimen name="Callout.svelte" state="note · caution · honesty">
    <div class="stack stack-4">
      <Callout tone="note" label="Note">
        <p>Every figure in this view is derived from recorded match outcomes. Nothing is modelled or projected.</p>
      </Callout>
      <Callout tone="caution" label="Caution">
        <p>Changing region keeps the patch range but may empty the bracket selector: not every bracket is published in every region.</p>
      </Callout>
      <Callout tone="honesty" label="Sample size">
        <p>3 champion-role cells are withheld because they fall below the 500 game minimum. They are shown as withheld, never as zero.</p>
      </Callout>
    </div>
  </Specimen>

  <Specimen name="Banner.svelte" state="info · success · warning · danger · dismissible">
    <div class="stack stack-3">
      {#if bannerVisible}
        <Banner tone="info" dismissible onclose={() => (bannerVisible = false)}>
          Patch 16.19 publishes on 2026-09-11. The explorer still serves 16.18 until then.
        </Banner>
      {:else}
        <p class="muted">Dismissed. Reload the page to bring it back.</p>
      {/if}
      <Banner tone="success">Export finished: 141 rows written to CSV.</Banner>
      <Banner tone="warning">Rolling window closed early on 2026-09-02: ingestion was paused for 6 hours.</Banner>
      <Banner tone="danger">The published artifact could not be read. The view below is the last good revision.</Banner>
    </div>
  </Specimen>

  <Specimen name="Disclosure.svelte" state="closed with summary · open">
    <div class="stack stack-3">
      <Disclosure title="How is the win rate computed?" summary="Derived from recorded outcomes, not modelled">
        <p>
          Wins divided by games for that champion, role, patch, region and bracket. A cell is only
          published once it reaches 500 games; below that it is withheld.
        </p>
      </Disclosure>
      <Disclosure title="What does the confidence interval mean?" open>
        <p>
          The half-width of a 95% interval over the same sample. A wide interval means the sample is
          small, not that the champion is volatile.
        </p>
      </Disclosure>
    </div>
  </Specimen>

  <Specimen
    name="Tabs.svelte"
    state="three tabs · arrow-key navigation"
    note="Tabs swap panels of the same view. They never navigate between pages."
  >
    <Tabs items={tabItems} bind:value={tab} label="Champion detail sections">
      {#snippet panel(id)}
        {#if id === "summary"}
          <p>Summary panel: headline win rate, pick rate, ban rate and tier for the selected cell.</p>
        {:else if id === "matchups"}
          <p>Matchups panel: the worst and best opposing champions in the same role.</p>
        {:else}
          <p>Items and runes panel: the most built combinations for the selected cell.</p>
        {/if}
      {/snippet}
    </Tabs>
  </Specimen>

  <Specimen name="Dialog.svelte" state="closed · open with footer">
    <div class="cluster">
      <Button onclick={() => (dialogOpen = true)}>Open the dialog</Button>
      <span class="use">Native <code>&lt;dialog&gt;</code>: focus trap, Escape and the scrim come for free.</span>
    </div>
    <Dialog
      bind:open={dialogOpen}
      title="Reset every filter?"
      description="Region, queue, bracket, patch range and role selection return to their defaults."
    >
      <p>This cannot be undone, but the current view is still in your browser history.</p>
      {#snippet footer()}
        <div class="cluster cluster-spread">
          <Button variant="ghost" onclick={() => (dialogOpen = false)}>Cancel</Button>
          <Button variant="danger" onclick={() => (dialogOpen = false)}>Reset filters</Button>
        </div>
      {/snippet}
    </Dialog>
  </Specimen>

  <Specimen name="Drawer.svelte" state="end · start · with footer">
    <div class="cluster">
      <Button variant="secondary" onclick={() => (drawerEnd = true)}>Filters (end)</Button>
      <Button variant="secondary" onclick={() => (drawerStart = true)}>Sections (start)</Button>
    </div>
    <Drawer bind:open={drawerEnd} side="end" title="Filters">
      <p>Below 1024px the filter bar moves into a drawer so the table keeps the full width.</p>
      {#snippet footer()}
        <div class="cluster cluster-spread">
          <Button variant="ghost" onclick={() => (drawerEnd = false)}>Clear</Button>
          <Button onclick={() => (drawerEnd = false)}>Apply 4 filters</Button>
        </div>
      {/snippet}
    </Drawer>
    <Drawer bind:open={drawerStart} side="start" title="On this page">
      <p>Long reference pages put their contents list in a start-side drawer on small viewports.</p>
    </Drawer>
  </Specimen>

  <Specimen
    name="Tooltip.svelte"
    state="on a definition term"
    note="A tooltip repeats context. It never carries a figure or a caveat that exists nowhere else."
  >
    <p>
      Cells are withheld below the
      <Tooltip text="Minimum games in a cell before it is published: 500 for this artifact.">
        <span class="term">minimum sample</span>
      </Tooltip>.
    </p>
  </Specimen>

  <Specimen name="Popover.svelte" state="align end · align start · with content">
    <div class="cluster">
      <Popover label="Columns" align="end">
        <div class="pop-body">
          <p class="eyebrow">Visible columns</p>
          <p>Win %, pick %, ban %, tier, sample size. Toggle any of them; the table reflows without a reload.</p>
        </div>
      </Popover>
      <Popover label="Sort help" align="start">
        <p>Sorting is stable: ties keep their previous order, so two sorts compose predictably.</p>
      </Popover>
    </div>
  </Specimen>

  <Specimen name="Toast.svelte" state="success · neutral · warning · dismissible">
    <div class="toast-stage">
      <Toast items={toasts} ondismiss={(id) => (toasts = toasts.filter((t) => t.id !== id))}>
        <Button variant="ghost" onclick={() => (toasts = [...toasts])}>Restore toasts</Button>
      </Toast>
    </div>
  </Specimen>

  <Specimen name="Breadcrumbs.svelte" state="two links · current page">
    <Breadcrumbs items={crumbs} />
  </Specimen>

  <Specimen name="PageHeader.svelte" state="eyebrow · lede · meta · actions · children" note="rendered with level=2 because this specimen lives inside a page that already has an h1">
    <PageHeader
      eyebrow="Explorer"
      title="Champion statistics"
      lede="Published champion-role cells for one patch, region, queue and bracket."
      level={2}
    >
      {#snippet meta()}
        <p class="num">Patch 16.18 · EUW · Ranked Solo · 141 cells · published 2026-09-04</p>
      {/snippet}
      {#snippet actions()}
        <Button variant="secondary">Export</Button>
        <Button variant="ghost">Copy link</Button>
      {/snippet}
    </PageHeader>
  </Specimen>

  <Specimen name="SectionHeader.svelte" state="level 2 · with aside">
    <SectionHeader title="Win rate by role">
      {#snippet aside()}
        <span class="num">141 rows</span>
      {/snippet}
    </SectionHeader>
  </Specimen>

  <Specimen name="Pagination.svelte" state="middle page · with total">
    <Pagination page={page} pageCount={9} total={141} onchange={(next) => (page = next)} />
  </Specimen>
</div>

<style>
  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
  }

  .flush-body {
    padding: var(--space-4);
  }

  .muted {
    color: var(--c-text-muted);
    margin: 0;
  }

  .use {
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }

  .use code {
    font-family: var(--font-mono);
    background: var(--tint-accent);
  }

  .tiles {
    display: grid;
    gap: var(--space-6);
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    justify-items: center;
    padding-block: var(--space-3);
  }

  .term {
    text-decoration: underline dotted 2px var(--c-text-accent);
    text-underline-offset: 3px;
    cursor: help;
  }

  .pop-body {
    max-inline-size: 22rem;
  }

  .toast-stage {
    position: relative;
    min-block-size: 9rem;
    display: grid;
    align-content: end;
    justify-items: end;
  }
</style>
