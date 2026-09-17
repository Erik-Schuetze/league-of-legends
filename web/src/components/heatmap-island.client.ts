/*
 * Client runtime for HeatmapIsland.
 *
 * The matrix, every value in it and the explanation of what a dash means are
 * server-rendered: with JavaScript disabled the page still answers "how does
 * this champion do against that one". This module adds three things on top of
 * that markup and creates none of it -
 *
 *   1. a roving tabindex, so the grid is one tab stop with arrow-key movement;
 *   2. the detail line, filled from the cell's own text and data attributes;
 *   3. hover and tap, which write the same line.
 *
 * It reads only what is already in the DOM, so the no-JavaScript page and the
 * enhanced page can never disagree about a number.
 */

type Cell = HTMLTableCellElement;

export function initHeatmapIsland(root: HTMLElement): void {
  if (root.dataset.islandReady === 'true') return;

  const table = root.querySelector('table');
  const body = table?.tBodies[0];
  const detail = root.querySelector<HTMLElement>('[data-hm-detail]');
  if (!(table instanceof HTMLTableElement) || !(body instanceof HTMLTableSectionElement)) return;

  const bodyRows = Array.from(body.rows);
  const cells = Array.from(table.querySelectorAll<Cell>('td[data-n]'));
  if (bodyRows.length === 0 || cells.length === 0) return;

  const minN = root.getAttribute('data-min-n') ?? '';

  const head = table.tHead;
  const headerRow = head ? head.rows[head.rows.length - 1] : null;
  const columnNames: string[] = [];
  if (headerRow) {
    for (let index = 1; index < headerRow.cells.length; index += 1) {
      columnNames[index - 1] = (headerRow.cells[index].textContent ?? '').trim();
    }
  }
  const rowNames = bodyRows.map((row) => (row.cells[0]?.textContent ?? '').trim());

  const at = (row: number, column: number): Cell | null => {
    const cellsInRow = bodyRows[row]?.cells;
    const cell = cellsInRow ? cellsInRow[column + 1] : undefined;
    return cell instanceof HTMLTableCellElement ? cell : null;
  };

  const describe = (row: number, column: number): string => {
    const cell = at(row, column);
    const rowName = rowNames[row] ?? '';
    const columnName = columnNames[column] ?? '';
    if (!cell) return '';
    if (cell.classList.contains('missing')) {
      return `${rowName} against ${columnName}: not published, under ${minN} games in this window.`;
    }
    const rate = (cell.querySelector('.rate')?.textContent ?? '').trim();
    const deviation = (cell.querySelector('.dev')?.textContent ?? '').trim();
    const n = cell.getAttribute('data-n') ?? '';
    return (
      `${rowName} against ${columnName}: ${rate} win rate over ${n} games, ` +
      `${deviation} percentage points versus even.`
    );
  };

  const show = (row: number, column: number): void => {
    if (!detail) return;
    const text = describe(row, column);
    if (text !== '') detail.textContent = text;
  };

  let focusRow = 0;
  let focusColumn = 0;
  const columnCount = columnNames.length;

  const setTabstop = (): void => {
    for (const cell of cells) cell.tabIndex = -1;
    const current = at(focusRow, focusColumn);
    if (current) current.tabIndex = 0;
  };

  const moveTo = (row: number, column: number, focus: boolean): void => {
    focusRow = Math.min(Math.max(row, 0), bodyRows.length - 1);
    focusColumn = Math.min(Math.max(column, 0), Math.max(columnCount - 1, 0));
    setTabstop();
    if (focus) at(focusRow, focusColumn)?.focus();
    show(focusRow, focusColumn);
  };

  const positionOf = (cell: Cell): { row: number; column: number } | null => {
    const row = cell.parentElement;
    if (!(row instanceof HTMLTableRowElement)) return null;
    const index = bodyRows.indexOf(row);
    if (index < 0 || cell.cellIndex < 1) return null;
    return { row: index, column: cell.cellIndex - 1 };
  };

  table.addEventListener('keydown', (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTableCellElement) || !target.hasAttribute('data-n')) return;
    const position = positionOf(target);
    if (!position) return;
    focusRow = position.row;
    focusColumn = position.column;
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault();
        moveTo(focusRow, focusColumn + 1, true);
        break;
      case 'ArrowLeft':
        event.preventDefault();
        moveTo(focusRow, focusColumn - 1, true);
        break;
      case 'ArrowDown':
        event.preventDefault();
        moveTo(focusRow + 1, focusColumn, true);
        break;
      case 'ArrowUp':
        event.preventDefault();
        moveTo(focusRow - 1, focusColumn, true);
        break;
      case 'Home':
        event.preventDefault();
        moveTo(focusRow, 0, true);
        break;
      case 'End':
        event.preventDefault();
        moveTo(focusRow, columnCount - 1, true);
        break;
      case 'Enter':
      case ' ':
        event.preventDefault();
        show(focusRow, focusColumn);
        break;
      default:
        break;
    }
  });

  const onPointer = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof HTMLTableCellElement)) return;
    const cell = target.closest('td[data-n]');
    if (!(cell instanceof HTMLTableCellElement)) return;
    const position = positionOf(cell);
    if (position) show(position.row, position.column);
  };

  table.addEventListener('mouseover', onPointer);
  table.addEventListener('click', onPointer);
  table.addEventListener('focusin', (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTableCellElement)) return;
    const position = positionOf(target);
    if (position) {
      focusRow = position.row;
      focusColumn = position.column;
      show(position.row, position.column);
    }
  });

  root.dataset.islandReady = 'true';
  setTabstop();
}
