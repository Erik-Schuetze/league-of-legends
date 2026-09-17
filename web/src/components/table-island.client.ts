/*
 * Client runtime for TableIsland.
 *
 * TableIsland's bootstrap dynamically imports this module, so nothing here is
 * fetched until the island has scrolled into view. It is vanilla, it is small,
 * and - the important part - it never creates markup. The toolbar, the status
 * line, the table and every cell are server-rendered; this module reveals the
 * controls, moves the existing rows into a new order and rewrites the status
 * text. With JavaScript disabled the page still shows the same table in the same
 * order, just without the controls.
 */

type Direction = 'asc' | 'desc';

type SortValue = number | string | null;

/** `""` means "not published" and sorts last, exactly like a missing attribute. */
const readValue = (row: HTMLTableRowElement, column: string): SortValue => {
  const raw = row.getAttribute(`data-v-${column}`);
  if (raw === null || raw === '') return null;
  const asNumber = Number(raw);
  return Number.isFinite(asNumber) ? asNumber : raw;
};

const compareValues = (left: SortValue, right: SortValue, direction: Direction): number => {
  // The missing value is not the smallest number, so it goes last in both
  // directions rather than opening a descending sort with a wall of dashes.
  if (left === null || right === null) {
    if (left === right) return 0;
    return left === null ? 1 : -1;
  }
  const sign = direction === 'asc' ? 1 : -1;
  if (typeof left === 'number' && typeof right === 'number') {
    if (left === right) return 0;
    return left < right ? -sign : sign;
  }
  const collated = String(left).localeCompare(String(right), 'en');
  return collated === 0 ? 0 : collated < 0 ? -sign : sign;
};

export function initTableIsland(root: HTMLElement): void {
  if (root.dataset.islandReady === 'true') return;

  const table = root.querySelector('table');
  const body = table?.querySelector('tbody');
  if (!(table instanceof HTMLTableElement) || !(body instanceof HTMLTableSectionElement)) return;

  const rows = Array.from(body.rows).filter((row) => row.hasAttribute('data-search'));
  if (rows.length === 0) return;

  const controls = root.querySelector<HTMLElement>('[data-ti-controls]');
  const filter = root.querySelector<HTMLInputElement>('[data-ti-filter]');
  const select = root.querySelector<HTMLSelectElement>('[data-ti-sort]');
  const toggle = root.querySelector<HTMLButtonElement>('[data-ti-dir]');
  const status = root.querySelector<HTMLElement>('[data-ti-status]');
  const headers = table.tHead ? Array.from(table.tHead.rows[0]?.cells ?? []) : [];

  let key = select instanceof HTMLSelectElement && select.value !== '' ? select.value : 'tier';
  let direction: Direction = toggle?.dataset.dir === 'asc' ? 'asc' : 'desc';

  const labelOf = (value: string): string => {
    const option = select?.querySelector<HTMLOptionElement>(`option[value="${value}"]`);
    return option?.textContent?.trim() ?? value;
  };

  const render = (): void => {
    const query = filter?.value.trim().toLowerCase() ?? '';
    let shown = 0;
    for (const row of rows) {
      const haystack = row.getAttribute('data-search') ?? '';
      const match = query === '' || haystack.includes(query);
      row.hidden = !match;
      if (match) shown += 1;
    }

    for (const row of [...rows].sort((left, right) => compareValues(readValue(left, key), readValue(right, key), direction))) {
      // append() moves the live node, so the listener-free server markup is
      // reordered in place and no element is ever constructed here.
      body.append(row);
    }

    for (const header of headers) {
      if (header.getAttribute('data-col') === key) {
        header.setAttribute('aria-sort', direction === 'asc' ? 'ascending' : 'descending');
      } else {
        header.removeAttribute('aria-sort');
      }
    }

    if (toggle instanceof HTMLButtonElement) {
      toggle.dataset.dir = direction;
      toggle.textContent = direction === 'asc' ? 'Ascending' : 'Descending';
      toggle.setAttribute(
        'aria-label',
        `Sort direction: ${direction === 'asc' ? 'ascending' : 'descending'}. Activate to reverse.`,
      );
    }

    if (status) {
      const hidden = rows.length - shown;
      status.textContent =
        `Showing ${shown} of ${rows.length} rows, sorted by ${labelOf(key)}, ` +
        `${direction === 'asc' ? 'ascending' : 'descending'}.` +
        (hidden > 0 ? ` ${hidden} hidden by the filter.` : '');
    }
  };

  if (controls) controls.hidden = false;

  filter?.addEventListener('input', render);
  select?.addEventListener('change', () => {
    if (select.value !== '') key = select.value;
    render();
  });
  toggle?.addEventListener('click', () => {
    direction = direction === 'asc' ? 'desc' : 'asc';
    render();
  });

  root.dataset.islandReady = 'true';
  render();
}
