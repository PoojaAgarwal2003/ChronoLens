export type Engine = 'row' | 'columnar' | 'indexed';

export interface Meta {
  dataset: string;
  input_format: 'jsonl' | 'snapshot';
  rows: number;
  service_count: number;
  services: string[];
  services_truncated: boolean;
  statuses: number[];
  min_timestamp_us: string | null;
  max_timestamp_us: string | null;
  engines: Engine[];
  default_buckets: number;
  max_buckets: number;
  load_ms: number | null;
}

export interface Query {
  engine: Engine;
  from_us: string;
  to_us: string | null;
  service: string;
  status: number;
  buckets: number;
}

export interface Aggregate {
  count: number;
  error_count: number;
  duration_sum_us: string;
  mean_duration_us: number | null;
  min_duration_us: number | null;
  max_duration_us: number | null;
}

export interface Stats {
  engine: string;
  total_rows: number;
  rows_examined: number;
  rows_skipped: number;
  index_comparisons: number;
}

export interface QueryResponse {
  result: { aggregate: Aggregate; stats: Stats };
  profile: {
    timeline: { from_us: string; to_us: string; count: number; error_count: number }[];
    histogram: { label: string; count: number }[];
    services: { service: string; count: number; error_count: number; mean_duration_us: number }[];
    services_truncated: boolean;
    rows_examined: number;
    index_comparisons: number;
  };
  timing: { query_ms: number | null; profile_ms: number | null; server_work_ms: number | null };
}

export interface Comparison {
  equivalent: boolean;
  results: {
    engine: string;
    aggregate: Aggregate;
    stats: Stats;
    iterations: number;
    mean_query_ms: number | null;
    measurement_ms: number | null;
    stable_window: boolean;
  }[];
}

export async function request<T>(path: string, signal: AbortSignal, body?: Query): Promise<T> {
  const response = await fetch(path, {
    signal,
    ...(body ? {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    } : {}),
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new Error(payload?.error?.message ?? `Request failed (${response.status}). Please retry.`);
  }
  return response.json() as Promise<T>;
}

// Slider units are tenths of a percent. No timestamp crosses through Number.
export function timeBounds(meta: Pick<Meta, 'min_timestamp_us' | 'max_timestamp_us'>, start: number, end: number) {
  const min = BigInt(meta.min_timestamp_us ?? '0');
  const max = BigInt(meta.max_timestamp_us ?? '0');
  const span = max - min + 1n;
  return {
    from_us: (min + span * BigInt(start) / 1000n).toString(),
    to_us: end === 1000 ? null : (min + span * BigInt(end) / 1000n).toString(),
  };
}

export const number = (value: number) => value.toLocaleString('en-US');
export const percent = (value: number) => `${value.toLocaleString('en-US', { maximumFractionDigits: 1 })}%`;
export const milliseconds = (value: number | null) => value === null ? 'Below clock resolution' : `${value.toLocaleString('en-US', { maximumSignificantDigits: 5 })} ms`;
export const duration = (value: number | null) => value === null ? 'No observations' : value >= 1000 ? `${(value / 1000).toLocaleString('en-US', { maximumFractionDigits: 2 })} ms` : `${value.toLocaleString('en-US', { maximumFractionDigits: 2 })} µs`;
export function timestamp(value: string): string {
  const micros = BigInt(value);
  const date = new Date(Number(micros / 1000n));
  if (!Number.isFinite(date.getTime())) return `${value} µs`;
  return `${date.toISOString().replace(/\.\d{3}Z$/, '')}.${(micros % 1000000n).toString().padStart(6, '0')}Z`;
}
