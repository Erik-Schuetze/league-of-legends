// Number and date formatting for published values.
//
// Grouping is done by hand rather than with Intl/toLocaleString: the build is
// the only place these numbers are ever produced, and a locale-dependent
// formatter would let two build environments emit different HTML for the same
// artifact. Deterministic output is worth twenty lines here.

/** 0.5118 -> "51.18%". The rate stays a fraction in the artifact; only the view scales it. */
export function percent(value: number, digits = 2): string {
  return `${(value * 100).toFixed(digits)}%`;
}

/** Percentage points, with an explicit sign, for deltas. */
export function signedPercentPoints(value: number, digits = 2): string {
  const points = value * 100;
  const sign = points > 0 ? '+' : points < 0 ? '-' : '';
  return `${sign}${Math.abs(points).toFixed(digits)} pp`;
}

/** 8421 -> "8,421". */
export function integer(value: number): string {
  const rounded = Math.round(value);
  const sign = rounded < 0 ? '-' : '';
  const digits = Math.abs(rounded).toString();
  let out = '';
  for (let i = 0; i < digits.length; i += 1) {
    const fromEnd = digits.length - i;
    out += digits[i];
    if (fromEnd > 1 && (fromEnd - 1) % 3 === 0) out += ',';
  }
  return sign + out;
}

export function decimal(value: number, digits = 2): string {
  return value.toFixed(digits);
}

/** Rounds to whole percent points, for a scan-friendly summary. */
export function wholePercent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

/** "2026-09-17T02:14:00Z" -> "2026-09-17". Dates already come as YYYY-MM-DD. */
export function isoDate(value: string): string {
  return value.slice(0, 10);
}

/** An ISO instant rendered as a UTC label, so the reader knows what it is. */
export function utcStamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (n: number) => n.toString().padStart(2, '0');
  return (
    `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}` +
    ` ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())} UTC`
  );
}

/** "2026-09-03 to 2026-09-17", for a source window. */
export function windowLabel(from: string, to: string): string {
  return `${isoDate(from)} to ${isoDate(to)}`;
}

/**
 * The 95% confidence half-width in percentage points. Reported next to a rate
 * so the reader can see how much precision the sample actually bought.
 */
export function plusMinus(halfWidth: number, digits = 2): string {
  return `+/- ${(halfWidth * 100).toFixed(digits)} pp`;
}

/** Median of a sample-size series, used for the honesty notice on aggregate pages. */
export function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 1 ? sorted[mid] : Math.round((sorted[mid - 1] + sorted[mid]) / 2);
}
