<script lang="ts">
  import Specimen from "./Specimen.svelte";
  import BarMeter from "$lib/components/BarMeter.svelte";
  import Button from "$lib/components/Button.svelte";
  import Card from "$lib/components/Card.svelte";
  import ChampionLockup from "$lib/components/ChampionLockup.svelte";
  import DataTable from "$lib/components/DataTable.svelte";
  import DeltaIndicator from "$lib/components/DeltaIndicator.svelte";
  import HeatmapMatrix from "$lib/components/HeatmapMatrix.svelte";
  import RoleBadge from "$lib/components/RoleBadge.svelte";
  import SampleAnnotation from "$lib/components/SampleAnnotation.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import StatTile from "$lib/components/StatTile.svelte";
  import TierBadge from "$lib/components/TierBadge.svelte";
  import type { Column, Sort } from "$lib/components/table";
  import type { Role } from "$lib/components/RoleBadge.svelte";
  import type { Tier } from "$lib/components/TierBadge.svelte";

  interface DemoRow extends Record<string, unknown> {
    champion: string;
    slug: string;
    id: number;
    role: Role;
    win_rate: string;
    pick_rate: string;
    ban_rate: string;
    n: number;
    tier: Tier;
    ci: string;
  }

  const tierOrder: Tier[] = ["S+", "S", "A", "B", "C", "D"];
  const roleOrder: Role[] = ["TOP", "JUNGLE", "MID", "BOTTOM", "SUPPORT"];

  const rows: DemoRow[] = [
    { champion: "Aatrox", slug: "aatrox", id: 266, role: "TOP", win_rate: "53.1%", pick_rate: "12.4%", ban_rate: "18.9%", n: 12480, tier: "S+", ci: "±0.9pp" },
    { champion: "Ahri", slug: "ahri", id: 103, role: "MID", win_rate: "52.0%", pick_rate: "9.8%", ban_rate: "6.1%", n: 15204, tier: "S", ci: "±0.8pp" },
    { champion: "Amumu", slug: "amumu", id: 32, role: "JUNGLE", win_rate: "51.4%", pick_rate: "5.2%", ban_rate: "3.4%", n: 8905, tier: "A", ci: "±1.1pp" },
    { champion: "Ashe", slug: "ashe", id: 22, role: "BOTTOM", win_rate: "50.2%", pick_rate: "8.1%", ban_rate: "2.9%", n: 11302, tier: "B", ci: "±1.0pp" },
    { champion: "Alistar", slug: "alistar", id: 12, role: "SUPPORT", win_rate: "49.7%", pick_rate: "6.4%", ban_rate: "1.7%", n: 7410, tier: "C", ci: "±1.3pp" }
  ];

  const columns: Column[] = [
    { key: "champion", label: "Champion", sortable: true, width: "14rem" },
    { key: "role", label: "Role", sortable: true, width: "7rem" },
    { key: "n", label: "Games", numeric: true, sortable: true, hint: "Games behind this figure" },
    { key: "win_rate", label: "Win", numeric: true, sortable: true },
    { key: "pick_rate", label: "Pick", numeric: true, sortable: true },
    { key: "ban_rate", label: "Ban", numeric: true, sortable: true },
    { key: "ci", label: "95% CI", numeric: true, hint: "Half-width of the 95% interval" },
    { key: "tier", label: "Tier", sortable: true, width: "6rem" }
  ];

  let sort = $state<Sort>({ key: "win_rate", dir: "desc" });

  const heatRows = [
    { name: "Aatrox", slug: "aatrox", cells: { TOP: { value: 0.531, n: 12480 }, JUNGLE: { suppressed: true, n: 120 }, MID: { absent: true }, BOTTOM: { absent: true }, SUPPORT: { absent: true } } },
    { name: "Ahri", slug: "ahri", cells: { TOP: { suppressed: true, n: 240 }, JUNGLE: { absent: true }, MID: { value: 0.52, n: 15204 }, BOTTOM: { value: 0.487, n: 640 }, SUPPORT: { absent: true } } },
    { name: "Amumu", slug: "amumu", cells: { TOP: { suppressed: true, n: 310 }, JUNGLE: { value: 0.514, n: 8905 }, MID: { absent: true }, BOTTOM: { absent: true }, SUPPORT: { value: 0.501, n: 2180 } } },
    { name: "Ashe", slug: "ashe", cells: { TOP: { absent: true }, JUNGLE: { suppressed: true, n: 190 }, MID: { value: 0.494, n: 1520 }, BOTTOM: { value: 0.502, n: 11302 }, SUPPORT: { value: 0.547, n: 3310 } } },
    { name: "Alistar", slug: "alistar", cells: { TOP: { suppressed: true, n: 95 }, JUNGLE: { suppressed: true, n: 140 }, MID: { absent: true }, BOTTOM: { suppressed: true, n: 260 }, SUPPORT: { value: 0.497, n: 7410 } } }
  ];
</script>

<div class="stack stack-8">
  <Specimen
    name="StatTile.svelte"
    state="plain · with note · with delta · linked · withheld · loading"
    note="Every statistic carries its sample. A tile without n is only allowed for counts of published cells."
  >
    <div class="grid">
      <StatTile label="Published cells" value="141" note="Champion-role pairs above the sample floor." />
      <StatTile
        label="Median win rate"
        value="50.4"
        unit="%"
        n={12480}
        minCellN={500}
        note="Across all published cells in this partition."
      />
      <StatTile
        label="Top win rate"
        value="53.1"
        unit="%"
        n={12480}
        minCellN={500}
        delta={{ value: 1.2, unit: "pp", baseline: "vs 16.17", decimals: 1 }}
        note="Aatrox, top."
        href="#gallery"
      />
      <StatTile label="Lowest pick rate" value="0.4" unit="%" n={612} minCellN={500} delta={{ value: -0.3, unit: "pp", baseline: "vs 16.17", decimals: 1 }} />
      <StatTile label="Worst matchup" value="—" withheld n={210} minCellN={500} note="Aatrox into Kennen: 210 games." />
      <StatTile label="Loading stat" value="" loading />
    </div>
  </Specimen>

  <Specimen name="DeltaIndicator.svelte" state="up · down · flat · inverted (lower is better) · sm">
    <div class="cluster cluster-5">
      <DeltaIndicator value={1.24} unit="pp" baseline="vs 16.17" />
      <DeltaIndicator value={-0.86} unit="pp" baseline="vs 16.17" />
      <DeltaIndicator value={0} unit="pp" baseline="vs 16.17" />
      <DeltaIndicator value={-0.86} unit="pp" baseline="vs 16.17" inverted />
      <DeltaIndicator value={1.24} unit="pp" baseline="vs 16.17" size="sm" />
    </div>
    <p class="use">
      Sign, arrow and the baseline in words. The baseline is on screen, not only in the accessible name:
      a delta with no stated baseline is not a fact.
    </p>
  </Specimen>

  <Specimen name="BarMeter.svelte" state="neutral · high-good · low-good · with reference · withheld · sm">
    <div class="stack stack-4">
      <BarMeter label="Win rate · Aatrox TOP" value={0.531} text="53.1%" n={12480} minCellN={500} />
      <BarMeter label="Above baseline is good" value={0.531} text="53.1%" polarity="high-good" n={12480} reference={{ at: 0.5, label: "even" }} />
      <BarMeter label="Death share, lower is better" value={0.21} text="21.0%" polarity="low-good" n={8905} />
      <BarMeter label="Highest ban rate" value={0.189} text="18.9%" n={12480} size="sm" />
      <BarMeter label="Withheld cell" value={undefined} text="—" withheld n={210} minCellN={500} />
    </div>
  </Specimen>

  <Specimen
    name="HeatmapMatrix.svelte"
    state="published · withheld · absent"
    flag="honesty"
    note="The number is always printed inside the cell, so the tint is a scanning aid and never the value. Withheld and absent are drawn differently."
  >
    <HeatmapMatrix
      rows={heatRows}
      columns={roleOrder}
      caption="Win rate by champion and role"
      legend="Tint steps at ±2pp, ±4pp and beyond from an even 50%. A hatched cell is withheld below the sample floor; a dash means no such role exists in this slice."
    />
  </Specimen>

  <Specimen name="TierBadge.svelte" state="S+ · S · A · B · C · D · with note · sm">
    <div class="cluster cluster-2">
      {#each tierOrder as tier (tier)}
        <TierBadge {tier} />
      {/each}
      <TierBadge tier="S+" note="top 4%" />
      <TierBadge tier="D" size="sm" />
    </div>
    <p class="use">
      Six tiers, one accent tint and a weight ramp: no rainbow. It survives greyscale printing and every
      colour-vision deficiency, and it is announced as "tier S plus", not as a glyph.
    </p>
  </Specimen>

  <Specimen name="RoleBadge.svelte" state="word · glyph · sm">
    <div class="cluster cluster-2">
      {#each roleOrder as role (role)}
        <RoleBadge {role} />
      {/each}
    </div>
    <div class="cluster cluster-2">
      {#each roleOrder as role (role)}
        <RoleBadge {role} variant="glyph" size="sm" />
      {/each}
      <span class="use">Glyph only in dense tables, where the column header carries the words.</span>
    </div>
  </Specimen>

  <Specimen name="ChampionLockup.svelte" state="linked · unpublished · sm · lg · with id">
    <div class="cluster cluster-5">
      <ChampionLockup name="Aatrox" slug="aatrox" id={266} href="#gallery" />
      <ChampionLockup name="Aurelion Sol" slug="aurelion-sol" id={136} href="#gallery" size="lg" />
      <ChampionLockup name="Nunu & Willump" slug="nunu" id={20} href="#gallery" size="sm" />
      <ChampionLockup name="Kennen" slug="kennen" id={85} published={false} />
    </div>
    <p class="use">
      No icon is fetched: every champion is a monogram tile, which is also why nothing about the lockup
      depends on a network request at render time.
    </p>
  </Specimen>

  <Specimen name="SampleAnnotation.svelte" state="plain · minimum stated · with population · withheld · sm">
    <div class="stack stack-3">
      <SampleAnnotation n={12480} />
      <SampleAnnotation n={12480} minCellN={500} />
      <SampleAnnotation n={141} of={8805} minCellN={500} />
      <SampleAnnotation n={210} minCellN={500} withheld />
      <SampleAnnotation n={612} size="sm" />
    </div>
  </Specimen>

  <Specimen
    name="DataTable.svelte"
    state="sortable · numeric alignment · sticky header · dense · loading"
    note="A snippet authors the cells, so the page owns the champions, tiers and role badges inside rows."
  >
    <Card flush>
      <div class="table-body">
        <DataTable {columns} rows={rows} {sort} onsort={(next) => (sort = next)} caption="Published champion-role cells, patch 16.18, EU West, ranked solo">
          {#snippet row(item, index)}
            <td><ChampionLockup name={item.champion} slug={item.slug} id={item.id} href="#gallery" size="sm" /></td>
            <td><RoleBadge role={item.role} variant="glyph" size="sm" /></td>
            <td class="cell-num">{item.n.toLocaleString("en-GB")}</td>
            <td class="cell-num">{item.win_rate}</td>
            <td class="cell-num">{item.pick_rate}</td>
            <td class="cell-num">{item.ban_rate}</td>
            <td class="cell-num">{item.ci}</td>
            <td><TierBadge tier={item.tier} size="sm" /></td>
          {/snippet}
        </DataTable>
      </div>
    </Card>
    <Card flush>
      <div class="table-body">
        <DataTable {columns} rows={[] as DemoRow[]} caption="Dense variant with the same headers" dense state="loading">
          {#snippet row(item)}
            <td>{item.champion}</td>
          {/snippet}
        </DataTable>
      </div>
    </Card>
  </Specimen>

  <Specimen name="DataTable.svelte + Card.svelte" state="a table inside a card, edge to edge">
    <Card>
      <SectionHeader title="Cells above the sample floor">
        {#snippet aside()}
          <span class="num">141 rows</span>
        {/snippet}
      </SectionHeader>
      <p class="num">Every number in the table is tabular so columns never jitter while sorting.</p>
      <div class="cluster">
        <Button variant="ghost" size="sm">Export CSV</Button>
        <Button variant="ghost" size="sm">Copy link</Button>
      </div>
    </Card>
  </Specimen>
</div>

<style>
  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr));
  }

  .use {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
    max-inline-size: 76ch;
  }

  .table-body {
    overflow-x: auto;
  }

  .table-body :global(.cell-num) {
    text-align: end;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums lining-nums;
  }
</style>
