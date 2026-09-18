<script lang="ts">
  import Banner from "$lib/components/Banner.svelte";
  import Button from "$lib/components/Button.svelte";
  import Card from "$lib/components/Card.svelte";
  import ChampionLockup from "$lib/components/ChampionLockup.svelte";
  import Callout from "$lib/components/Callout.svelte";
  import Chip from "$lib/components/Chip.svelte";
  import Combobox from "$lib/components/Combobox.svelte";
  import CopyButton from "$lib/components/CopyButton.svelte";
  import CoverageStamp from "$lib/components/CoverageStamp.svelte";
  import DataTable from "$lib/components/DataTable.svelte";
  import Drawer from "$lib/components/Drawer.svelte";
  import EmptyState from "$lib/components/EmptyState.svelte";
  import ExportMenu from "$lib/components/ExportMenu.svelte";
  import FilterBar from "$lib/components/FilterBar.svelte";
  import FilterChip from "$lib/components/FilterChip.svelte";
  import HeatmapMatrix from "$lib/components/HeatmapMatrix.svelte";
  import MultiSelect from "$lib/components/MultiSelect.svelte";
  import NoPublishedData from "$lib/components/NoPublishedData.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import Pagination from "$lib/components/Pagination.svelte";
  import PatchRangePicker from "$lib/components/PatchRangePicker.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import RangeSlider from "$lib/components/RangeSlider.svelte";
  import RoleBadge from "$lib/components/RoleBadge.svelte";
  import SampleAnnotation from "$lib/components/SampleAnnotation.svelte";
  import SearchField from "$lib/components/SearchField.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import SegmentedControl from "$lib/components/SegmentedControl.svelte";
  import Select from "$lib/components/Select.svelte";
  import StatTile from "$lib/components/StatTile.svelte";
  import StalenessIndicator from "$lib/components/StalenessIndicator.svelte";
  import SuppressionSummary from "$lib/components/SuppressionSummary.svelte";
  import Switch from "$lib/components/Switch.svelte";
  import TierBadge from "$lib/components/TierBadge.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import type { Column, Sort } from "$lib/components/table";
  import type { Role } from "$lib/components/RoleBadge.svelte";
  import {
    cells,
    championById,
    coverage,
    dataSource,
    generatedAt,
    latestPatch,
    partitions,
    patchesAvailable,
    roles as allRoles,
    tiers as allTiers,
    unpublishedPairs
  } from "$lib/data/demo.generated";
  import type { Cell, Champion } from "$lib/data/demo.generated";

  /* -- Filter state ---------------------------------------------------------
     Every filter lives in one object so a view is a single shareable unit. In
     the product these values come from the URL; the mockup keeps them in
     component state and renders the link the URL would carry. */

  let region = $state("EUW");
  let queue = $state(420);
  let bracket = $state("all");
  let roleSelection = $state<string[]>([...allRoles]);
  let tierSelection = $state<string[]>([...allTiers]);
  let patchFrom = $state("16.18");
  let patchTo = $state("16.18");
  let query = $state("");
  let championQuery = $state("");
  let minGames = $state(500);
  let includeUnpublished = $state(false);
  let view = $state("table");
  let sort = $state<Sort>({ key: "win_rate", dir: "desc" });
  let page = $state(1);
  let filtersOpen = $state(false);
  let toasts = $state<{ id: string; tone?: "neutral" | "success" | "warning"; message: string }[]>([]);
  let exportNote = $state("");

  /* The publisher has a partition for the newest patch in this region only. The
     rest of the selector exists so the "not published" state is reachable. */
  const publishedRegions = new Set<string>(partitions.map((p) => p.region));
  const publishedQueueIds = new Set<number>(partitions.map((p) => p.queue));
  const slicePublished = $derived.by(
    () =>
      publishedRegions.has(region) &&
      publishedQueueIds.has(queue) &&
      partitions.some((p) => p.patch === patchFrom)
  );

  const allCellRows: Array<{ cell: Cell; champion: Champion }> = $derived(
    cells.flatMap((cell) => {
      const champion = championById.get(cell.champion_id);
      return champion ? [{ cell, champion }] : [];
    })
  );

  const withheldRows = $derived(
    unpublishedPairs.slice(0, 24).map((pair) => {
      const champion = championById.get(pair.champion_id);
      return { pair, champion };
    })
  );

  const rows = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    const matches = allCellRows.filter(({ cell, champion }) => {
      if (!roleSelection.includes(cell.role)) return false;
      if (!tierSelection.includes(cell.tier)) return false;
      if (cell.n < minGames) return false;
      if (championQuery && champion.id !== Number(championQuery)) return false;
      if (needle) {
        const haystack = `${champion.name} ${champion.slug} ${cell.role} ${cell.tier}`.toLowerCase();
        if (!haystack.includes(needle)) return false;
      }
      return true;
    });

    const dir = sort.dir === "asc" ? 1 : -1;
    const key = sort.key;
    return [...matches].sort((a, b) => {
      if (key === "champion") return a.champion.name.localeCompare(b.champion.name) * dir;
      if (key === "role") return a.cell.role.localeCompare(b.cell.role) * dir;
      if (key === "tier") return a.cell.tier.localeCompare(b.cell.tier) * dir;
      if (key === "n") return (a.cell.n - b.cell.n) * dir;
      if (key === "ci") return (a.cell.ci95_half_width - b.cell.ci95_half_width) * dir;
      const field = key as "win_rate" | "pick_rate" | "ban_rate";
      return (a.cell[field] - b.cell[field]) * dir;
    });
  });

  const pageSize = 25;
  const pageCount = $derived(Math.max(1, Math.ceil(rows.length / pageSize)));
  const pagedRows = $derived(rows.slice((Math.min(page, pageCount) - 1) * pageSize, Math.min(page, pageCount) * pageSize));

  const activeFilters = $derived.by(() => {
    const list: { label: string; value: string }[] = [];
    if (region !== "EUW") list.push({ label: "Region", value: region });
    if (queue !== 420) list.push({ label: "Queue", value: String(queue) });
    if (bracket !== "all") list.push({ label: "Bracket", value: bracket });
    if (roleSelection.length !== allRoles.length) list.push({ label: "Roles", value: roleSelection.join(", ") });
    if (tierSelection.length !== allTiers.length) list.push({ label: "Tiers", value: tierSelection.join(", ") });
    if (patchFrom !== "16.18" || patchTo !== "16.18") list.push({ label: "Patches", value: `${patchFrom} → ${patchTo}` });
    if (minGames !== 500) list.push({ label: "Min games", value: minGames.toLocaleString("en-GB") });
    if (query) list.push({ label: "Search", value: query });
    return list;
  });

  const shareUrl = $derived(
    `https://lolstats.example/explore/?patch=${patchFrom}&region=${region}&queue=${queue}&bracket=${bracket}&roles=${roleSelection.join("+")}&min=${minGames}`
  );

  const heatRows = $derived(
    rows.slice(0, 12).map(({ cell, champion }) => ({
      name: champion.name,
      slug: champion.slug,
      cells: { [cell.role]: { value: cell.win_rate, n: cell.n } }
    }))
  );

  const suppressedHere = $derived(
    partitions.find((p) => p.patch === patchFrom && p.region === region && p.queue === queue)?.suppressed_cells ?? 0
  );

  /* The KPI row is derived as one object so that it exists only when there are
     cells to describe. A tile whose value is a placeholder dash is worse than no
     tile at all: the EmptyState below already says what happened, and a dash in
     the meantime reads as a withheld measurement. */
  const kpis = $derived.by(() => {
    if (!rows.length) return undefined;
    const sorted = rows.map((r) => r.cell.win_rate).sort((a, b) => a - b);
    const middle = Math.floor(sorted.length / 2);
    const median = sorted.length % 2 ? sorted[middle]! : (sorted[middle - 1]! + sorted[middle]!) / 2;
    return {
      median: (median * 100).toFixed(1),
      widest: (Math.max(...rows.map((r) => r.cell.ci95_half_width)) * 100).toFixed(1),
      top: rows[0]!,
    };
  });

  const championOptions = $derived(
    [...championById.values()]
      .slice(0, 40)
      .map((champion) => ({ value: String(champion.id), label: champion.name, note: champion.roles.join(" · ") }))
  );

  const columns: Column[] = [
    { key: "champion", label: "Champion", sortable: true, width: "15rem" },
    { key: "role", label: "Role", sortable: true, width: "6.5rem" },
    { key: "n", label: "Games", numeric: true, sortable: true, hint: "Games behind this figure" },
    { key: "win_rate", label: "Win", numeric: true, sortable: true },
    { key: "pick_rate", label: "Pick", numeric: true, sortable: true },
    { key: "ban_rate", label: "Ban", numeric: true, sortable: true },
    { key: "ci", label: "95% CI", numeric: true, sortable: true, hint: "Half-width of the 95 percent interval" },
    { key: "tier", label: "Tier", sortable: true, width: "6rem" }
  ];

  const pct = (v: number, decimals = 1) => `${(v * 100).toFixed(decimals)}%`;

  function clearFilters() {
    region = "EUW";
    queue = 420;
    bracket = "all";
    roleSelection = [...allRoles];
    tierSelection = [...allTiers];
    patchFrom = "16.18";
    patchTo = "16.18";
    minGames = 500;
    query = "";
    championQuery = "";
    includeUnpublished = false;
    page = 1;
  }

  function runExport(id: string) {
    exportNote = id;
    toasts = [
      ...toasts,
      { id: `export-${id}`, tone: "success", message: `Export prepared: ${rows.length} rows as ${id.toUpperCase()}.` }
    ];
  }
</script>

<svelte:head>
  <title>Data explorer · LoL Stats</title>
  <meta name="description" content="Browse every published champion-role cell for a patch, region, queue and bracket, with its sample size, confidence interval and coverage stamp." />
</svelte:head>

<div class="stack stack-7">
  <Banner tone="info" dismissible>
    Patch 16.19 publishes on 2026-09-11. This explorer still serves 16.18 until then.
  </Banner>

  <PageHeader
    eyebrow="Explorer"
    title="Champion statistics"
    lede="Every published champion-role cell for one patch, region, queue and bracket, with the sample behind each figure."
  >
    {#snippet meta()}
      <div class="meta-row">
        <CoverageStamp
          patch={patchFrom === patchTo ? patchFrom : `${patchFrom} → ${patchTo}`}
          region={region}
          queue={String(queue)}
          bracket={bracket === "all" ? "All ranks" : bracket}
          {generatedAt}
          sourceWindow={coverage.source_window}
          minCellN={coverage.min_cell_n}
        />
        <StalenessIndicator {generatedAt} />
      </div>
    {/snippet}
    {#snippet actions()}
      <ExportMenu
        scope={`Champion cells · patch ${patchFrom === patchTo ? patchFrom : `${patchFrom}→${patchTo}`} · ${region} · queue ${queue} · ${bracket}`}
        filename={`lolstats-cells-${patchFrom}-${region}-${queue}-${bracket}`}
        rowCount={rows.length}
        onexport={runExport}
        footnote="CSV and JSON carry the patch, region, queue, bracket, source window and minimum sample."
      />
      <CopyButton value={shareUrl} label="view link" />
      <Button variant="ghost" onclick={clearFilters} disabled={activeFilters.length === 0}>Reset</Button>
    {/snippet}
  </PageHeader>

  <FilterBar sticky>
    <div class="filters-wide">
      <SearchField label="Search cells" bind:value={query} placeholder="Champion, role or tier" />
      <Combobox label="Champion" options={championOptions} bind:value={championQuery} placeholder="Exact champion" emptyMessage="No champion matches" />
      <Select
        label="Region"
        value={region}
        onchange={(v) => {
          region = v;
          page = 1;
        }}
        options={[
          { value: "EUW", label: "EU West" },
          { value: "NA", label: "North America", disabled: true },
          { value: "KR", label: "Korea", disabled: true }
        ]}
        hint="Only EU West has a published partition."
      />
      <Select
        label="Queue"
        value={String(queue)}
        onchange={(v) => {
          queue = Number(v);
          page = 1;
        }}
        options={[
          { value: "420", label: "Ranked Solo/Duo" },
          { value: "440", label: "Ranked Flex", disabled: true }
        ]}
      />
      <Select
        label="Bracket"
        value={bracket}
        onchange={(v) => {
          bracket = v;
          page = 1;
        }}
        options={[
          { value: "all", label: "All ranks" },
          { value: "gold_plus", label: "Gold and above", disabled: true }
        ]}
        note="Other brackets are not published for this region."
      />
      <PatchRangePicker available={patchesAvailable} bind:from={patchFrom} bind:to={patchTo} />
      <MultiSelect label="Roles" options={allRoles.map((role) => ({ value: role, label: role }))} bind:value={roleSelection} />
      <MultiSelect label="Tiers" options={allTiers.map((tier) => ({ value: tier, label: tier }))} bind:value={tierSelection} />
      <RangeSlider
        label="Minimum games"
        bind:value={minGames}
        min={500}
        max={16000}
        step={100}
        format={(v) => `${v.toLocaleString("en-GB")} games`}
        hint="The published floor is 500."
      />
      <Switch bind:checked={includeUnpublished} label="Show pairs with no published cell" onLabel="shown" offLabel="hidden" />
      <div class="drawer-trigger">
        <Button variant="secondary" onclick={() => (filtersOpen = true)}>Filters</Button>
      </div>
    </div>
    {#snippet chips()}
      {#each activeFilters as filter (`${filter.label}-${filter.value}`)}
        <FilterChip
          label={filter.label}
          value={filter.value}
          onremove={() => {
            if (filter.label === "Search") query = "";
            else if (filter.label === "Min games") minGames = 500;
            else if (filter.label === "Patches") {
              patchFrom = "16.18";
              patchTo = "16.18";
            } else if (filter.label === "Roles") roleSelection = [...allRoles];
            else if (filter.label === "Tiers") tierSelection = [...allTiers];
            else if (filter.label === "Region") region = "EUW";
            else if (filter.label === "Queue") queue = 420;
            else if (filter.label === "Bracket") bracket = "all";
            page = 1;
          }}
        />
      {/each}
      <Button variant="ghost" size="sm" onclick={clearFilters} disabled={activeFilters.length === 0}>Clear all</Button>
    {/snippet}
    {#snippet summary()}
      <p class="summary num">{rows.length} of {cells.length} cells · n ≥ {minGames.toLocaleString("en-GB")}</p>
    {/snippet}
  </FilterBar>

  <Drawer bind:open={filtersOpen} side="end" title="Filters">
    <p class="drawer-note">
      On this viewport the filter controls live in a drawer so the table keeps the full width. The chips stay
      on the page either way.
    </p>
    {#snippet footer()}
      <div class="cluster cluster-spread">
        <Button variant="ghost" onclick={clearFilters}>Clear all</Button>
        <Button onclick={() => (filtersOpen = false)}>Apply {activeFilters.length || "no"} filters</Button>
      </div>
    {/snippet}
  </Drawer>

  {#if !slicePublished}
    <Card>
      <NoPublishedData
        availablePatches={patchesAvailable}
        requested={patchFrom}
        region={region}
        queue={String(queue)}
        bracket={bracket === "all" ? "All ranks" : bracket}
        suggestedHref={`/explore?patch=${latestPatch}`}
        suggestedLabel="Show the newest published patch"
      />
    </Card>
  {:else}
    {#if kpis}
      <div class="stats">
        <StatTile label="Cells in view" value={String(rows.length)} note={`of ${cells.length} published in this partition.`} />
        <StatTile
          label="Median win rate"
          value={kpis.median}
          unit="%"
          n={rows.reduce((sum, row) => sum + row.cell.n, 0)}
          minCellN={minGames}
          note="Unweighted across the cells in view."
          delta={{ value: 0.3, unit: "pp", baseline: `vs 16.17`, decimals: 1 }}
        />
        <StatTile
          label="Largest sample"
          value={kpis.top.cell.n.toLocaleString("en-GB")}
          note={`${kpis.top.champion.name}, ${kpis.top.cell.role}.`}
        />
        <StatTile
          label="Widest interval"
          value={`±${kpis.widest}`}
          unit="pp"
          n={rows.length}
          minCellN={minGames}
          note="The least certain cell in view. A wide interval is a small sample, not a volatile champion."
        />
      </div>
    {/if}

    <section class="results" aria-labelledby="results-title">
      <SectionHeader title="Published cells" id="results-title">
        {#snippet aside()}
          <SegmentedControl
            options={[
              { value: "table", label: "Table" },
              { value: "heatmap", label: "Heatmap" },
              { value: "both", label: "Both" }
            ]}
            bind:value={view}
            name="view-mode"
          />
        {/snippet}
      </SectionHeader>

      {#if rows.length === 0}
        <Card>
          <EmptyState
            what="cells"
            activeFilters={activeFilters}
            availableCount={{ rows: cells.length, noun: "cells" }}
            onclear={clearFilters}
          />
        </Card>
      {:else}
        {#if view === "table" || view === "both"}
          <Card flush>
            <DataTable
              {columns}
              rows={pagedRows}
              {sort}
              onsort={(next) => {
                sort = next;
                page = 1;
              }}
              caption={`Published champion-role cells for patch ${patchFrom}, ${region}, queue ${queue}, ${bracket}`}
            >
              {#snippet row(record)}
                <td>
                  <ChampionLockup
                    name={record.champion.name}
                    slug={record.champion.slug}
                    id={record.champion.id}
                    size="sm"
                  />
                </td>
                <td><RoleBadge role={record.cell.role} variant="glyph" size="sm" /></td>
                <td class="cell-num">{record.cell.n.toLocaleString("en-GB")}</td>
                <td class="cell-num">{pct(record.cell.win_rate)}</td>
                <td class="cell-num">{pct(record.cell.pick_rate)}</td>
                <td class="cell-num">{pct(record.cell.ban_rate)}</td>
                <td class="cell-num">±{(record.cell.ci95_half_width * 100).toFixed(1)}pp</td>
                <td><TierBadge tier={record.cell.tier} size="sm" /></td>
              {/snippet}
            </DataTable>
            <div class="table-foot">
              <p class="foot-note">
                <SampleAnnotation n={rows.reduce((sum, row) => sum + row.cell.n, 0)} minCellN={minGames} />
              </p>
              <Pagination page={Math.min(page, pageCount)} {pageCount} total={rows.length} onchange={(next) => (page = next)} label="Cell pages" />
            </div>
          </Card>
        {/if}

        {#if includeUnpublished && withheldRows.length}
          <Card flush>
            <div class="withheld-head">
              <p class="eyebrow">No published cell</p>
              <p class="muted">
                These champion-role pairs exist in the catalogue and were not published in this build. They
                are unknown, not zero, and they are kept out of every average above.
              </p>
            </div>
            <div class="withheld-wrap">
              <table class="withheld">
                <caption>Champion-role pairs without a published cell</caption>
                <thead>
                  <tr>
                    <th scope="col">Champion</th>
                    <th scope="col">Role</th>
                    <th scope="col">Games</th>
                    <th scope="col">Win</th>
                    <th scope="col">Tier</th>
                  </tr>
                </thead>
                <tbody>
                  {#each withheldRows as row (`${row.pair.champion_id}-${row.pair.role}`)}
                    <tr class="withheld-row">
                      <td>
                        {#if row.champion}
                          <ChampionLockup name={row.champion.name} slug={row.champion.slug} id={row.champion.id} size="sm" published={false} />
                        {:else}
                          <span class="num">Champion {row.pair.champion_id}</span>
                        {/if}
                      </td>
                      <td><RoleBadge role={row.pair.role} variant="glyph" size="sm" /></td>
                      <td class="cell-num" data-withheld="true">withheld</td>
                      <td class="cell-num" data-withheld="true">
                        <span aria-hidden="true">—</span>
                        <span class="visually-hidden">not applicable: the games were withheld</span>
                      </td>
                      <td><TierBadge tier="D" size="sm" note="n/a" /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </Card>
        {/if}

        {#if view === "heatmap" || view === "both"}
          <Card>
            <SectionHeader title="Win rate by champion and role" level={3} />
            <HeatmapMatrix
              rows={heatRows}
              columns={allRoles}
              caption={`Win rate for the first ${heatRows.length} cells in view`}
              legend="Tint steps at ±2pp, ±4pp and beyond from an even 50%. The number is always printed. A hatched cell is withheld; a dash means the pair is not in the catalogue."
            />
          </Card>
        {/if}
      {/if}
    </section>
  {/if}

  <div class="honesty-grid">
    <Card>
      <SectionHeader title="What this slice withholds" level={3} />
      <SuppressionSummary
        suppressedCells={suppressedHere}
        minCellN={coverage.min_cell_n}
        publishedCells={cells.length}
        unpublishedPairs={unpublishedPairs.length}
      />
      <div class="cluster">
        <Switch bind:checked={includeUnpublished} label="List the unpublished pairs" onLabel="listed" offLabel="hidden" />
      </div>
    </Card>
    <Card>
      <SectionHeader title="Method" level={3} />
      <ul class="method">
        <li>Wins divided by games, per champion, role, patch, region, queue and bracket.</li>
        <li>A cell is published once it reaches {coverage.min_cell_n.toLocaleString("en-GB")} games. Below that it is withheld.</li>
        <li>Tiers are assigned within the partition, so a tier is a rank, not a threshold.</li>
        <li>Pick and ban rates are shares of all games in the same window.</li>
      </ul>
      <ProvenanceLine source={dataSource} detail={`fixtures/site/v1 · build run ${coverage.build_run_id}`} />
    </Card>
  </div>

  {#if exportNote}
    <Callout tone="note" label="Export">
      <p class="num">
        Preparing {rows.length} rows as {exportNote.toUpperCase()} for patch {patchFrom}, {region}, queue {queue}.
        The file header repeats the coverage stamp, so the data is still self-describing once it leaves the site.
      </p>
    </Callout>
  {/if}

  <Toast items={toasts} ondismiss={(id) => (toasts = toasts.filter((t) => t.id !== id))} />

  <div class="visually-hidden" aria-live="polite">
    {rows.length} cells match the current filters out of {cells.length} published.
  </div>
</div>

<style>
  .meta-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: center;
  }

  .filters-wide {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: start;
  }

  .filters-wide > :global(*) {
    flex: 1 1 12rem;
    min-inline-size: 11rem;
  }

  /* Below 768px the control row would wrap into a wall of stacked inputs, so
     the drawer trigger takes over. */
  .drawer-trigger {
    display: none;
    flex: 0 0 auto;
  }

  @media (max-width: 767px) {
    .filters-wide > :global(*) {
      display: none;
    }
    .filters-wide > :global(.drawer-trigger) {
      display: block;
    }
  }

  .summary {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }

  .drawer-note,
  .muted {
    margin: 0;
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .stats {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  }

  .results {
    display: grid;
    gap: var(--space-5);
  }

  .table-foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: center;
    justify-content: space-between;
    padding: var(--space-3) var(--space-4);
    border-block-start: 1px solid var(--c-rule);
  }

  .foot-note {
    margin: 0;
  }

  .cell-num {
    text-align: end;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums lining-nums;
  }

  .cell-num[data-withheld="true"] {
    color: var(--sig-unknown);
    font-style: italic;
  }

  .withheld-head {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-4);
  }

  .withheld-wrap {
    overflow-x: auto;
  }

  table.withheld {
    inline-size: 100%;
    border-collapse: collapse;
    font-size: var(--step--1);
  }

  table.withheld caption {
    text-align: start;
    padding: var(--space-2) var(--space-4);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    text-transform: uppercase;
    letter-spacing: var(--ls-wide);
    color: var(--c-text-muted);
  }

  table.withheld th,
  table.withheld td {
    text-align: start;
    padding: var(--row-y) var(--cell-x);
    border-block-end: 1px solid var(--c-rule);
  }

  table.withheld thead th {
    background: var(--c-surface-sunken);
    border-block-end: 2px solid var(--c-rule-strong);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    text-transform: uppercase;
    letter-spacing: var(--ls-wide);
  }

  /* Withheld rows are tinted, never hidden: the treatment is the message. */
  .withheld-row {
    background: var(--sig-unknown-tint);
  }

  .honesty-grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  }

  .method {
    margin: 0;
    padding-inline-start: var(--space-5);
    display: grid;
    gap: var(--space-2);
    color: var(--c-text);
    line-height: var(--lh-body);
  }
</style>
