/**
 * Table types. They live outside the component because a component that
 * declares `generics` cannot also export its own interfaces, and the pages
 * need these names to type their column lists.
 */

export interface Column {
  /** Key in the row record. Also the sort key. */
  key: string;
  label: string;
  /** Right-aligns the column and switches it to tabular numerals. */
  numeric?: boolean;
  sortable?: boolean;
  /** Fixed width, e.g. "6rem" or "12ch". */
  width?: string;
  /** A clarification announced with the header. Never essential information:
   *  it is not visible, and a header is the wrong place to explain a metric. */
  hint?: string;
}

export interface Sort {
  key: string;
  dir: "asc" | "desc";
}
