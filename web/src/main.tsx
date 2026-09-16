import { StrictMode, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { duration, milliseconds, number, percent, request, timeBounds, timestamp, type Comparison, type Engine, type Meta, type Query, type QueryResponse } from './api';
import { Histogram, Timeline } from './Charts';
import './style.css';

const engineNames: Record<Engine, string> = { row: 'Row scan', columnar: 'Columnar scan', indexed: 'Indexed search' };
const formatNames: Record<Meta['input_format'], string> = { jsonl: 'JSONL', snapshot: 'SNAPSHOT' };
const engineDescriptions: Record<Engine, string> = {
  row: 'Scan every row in the immutable dataset.',
  columnar: 'Scan contiguous timestamp columns, then read matching fields.',
  indexed: 'Locate the time window with binary search; visit only candidates.',
};

function App() {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [metaError, setMetaError] = useState('');
  const [retryMeta, setRetryMeta] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setMetaError('');
    request<Meta>('/api/meta', controller.signal).then(result => {
      if (!controller.signal.aborted) setMeta(result);
    }).catch(error => {
      if (!controller.signal.aborted) setMetaError(error instanceof Error ? error.message : 'Unable to load dataset metadata.');
    });
    return () => controller.abort();
  }, [retryMeta]);

  return <>
    <a className="skip-link" href="#workbench">Skip to workbench</a>
    <header className="app-header">
      <a className="brand" href="/" aria-label="ChronoLens home"><span className="brand-mark" aria-hidden="true">◷</span>Chrono<span>Lens</span><span className="brand-divider" /><small>TELEMETRY LAB</small></a>
      <span className="local-badge"><span /> Local dataset · immutable</span>
    </header>
    <main id="workbench" tabIndex={-1}>
      <div className="page-heading"><div><p className="eyebrow">OBSERVE THE DATA. UNDERSTAND THE WORK.</p><h1>Every event. A clearer view.</h1><p className="lede">Explore a time window. See what each query engine actually does.</p></div><span className="edition">EXPLORER <b>04</b></span></div>
      {metaError ? <div className="notice error" role="alert"><h2>Dataset unavailable</h2><p>{metaError}</p><button onClick={() => setRetryMeta(value => value + 1)}>Retry connection</button></div> :
        !meta ? <div className="notice" role="status">Connecting to the local dataset…</div> :
          meta.rows === 0 ? <section className="notice empty" role="status">
            <h2>No events loaded</h2>
            <p>{meta.dataset} is empty. Generate or convert a nonempty dataset and restart the local server with its path and matching format.</p>
            <p>There are no timestamp bounds or measurements to display.</p>
          </section> : <Workbench meta={meta} />}
      <footer><span>CHRONOLENS / LOCAL TELEMETRY EXPLORER</span><span>No sampling. No remote services. Same data, three execution paths.</span></footer>
    </main>
  </>;
}

function Workbench({ meta }: { meta: Meta }) {
  const [engine, setEngine] = useState<Engine>('indexed');
  const [service, setService] = useState('');
  const [status, setStatus] = useState(0);
  const [start, setStart] = useState(0);
  const [end, setEnd] = useState(1000);
  const [retry, setRetry] = useState(0);
  const [debounce, setDebounce] = useState(false);
  const [data, setData] = useState<QueryResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [comparison, setComparison] = useState<Comparison | null>(null);
  const [comparing, setComparing] = useState(false);
  const [compareError, setCompareError] = useState('');
  const compareController = useRef<AbortController | null>(null);
  const bounds = timeBounds(meta, start, end);
  const query = useMemo<Query>(() => ({
    engine, from_us: bounds.from_us, to_us: bounds.to_us,
    service, status, buckets: Math.min(100, meta.max_buckets),
  }), [engine, bounds.from_us, bounds.to_us, service, status, meta.max_buckets]);

  useEffect(() => {
    const controller = new AbortController();
    compareController.current?.abort();
    setComparison(null);
    setComparing(false);
    setCompareError('');
    setData(null);
    setError('');
    setLoading(true);
    const dispatch = () => {
      request<QueryResponse>('/api/query', controller.signal, query).then(result => {
        if (!controller.signal.aborted) setData(result);
      }).catch(reason => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Query failed. Please retry.');
      }).finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    };
    const timer = debounce ? window.setTimeout(dispatch, 100) : undefined;
    if (!debounce) dispatch();
    return () => { window.clearTimeout(timer); controller.abort(); compareController.current?.abort(); };
  }, [query, retry, debounce]);

  async function compare() {
    compareController.current?.abort();
    const controller = new AbortController();
    compareController.current = controller;
    setComparing(true);
    setComparison(null);
    setCompareError('');
    try {
      const result = await request<Comparison>('/api/compare', controller.signal, query);
      if (!controller.signal.aborted) setComparison(result);
    } catch (reason) {
      if (!controller.signal.aborted) setCompareError(reason instanceof Error ? reason.message : 'Comparison failed.');
    } finally {
      if (!controller.signal.aborted) setComparing(false);
    }
  }

  const aggregate = data?.result.aggregate;
  const stats = data?.result.stats;
  const avoided = stats?.total_rows ? stats.rows_skipped / stats.total_rows * 100 : 0;
  return <>
    <section className="dataset-strip" aria-label="Loaded dataset"><div className="dataset-name"><span className="file-icon" aria-hidden="true">▤</span><div><span className="micro-label">ACTIVE DATASET / <span data-testid="input-format">{formatNames[meta.input_format] ?? 'UNKNOWN FORMAT'}</span></span><strong title={meta.dataset}>{meta.dataset}</strong></div></div><div><strong>{number(meta.rows)}</strong><span>events loaded</span></div><div><strong>{number(meta.service_count)}</strong><span>services</span></div><div><strong>{milliseconds(meta.load_ms)}</strong><span>initial load · not query time</span></div></section>

    <section className="panel controls" aria-labelledby="query-title">
      <div className="section-heading"><h2 id="query-title"><span className="section-number">01</span>Shape your query</h2><span className="muted">Half-open time range [start, end)</span></div>
      <div className="filter-grid">
        <div><label htmlFor="engine">Execution engine</label><select id="engine" value={engine} onChange={event => { setDebounce(false); setEngine(event.target.value as Engine); }}>{meta.engines.map(value => <option key={value} value={value}>{engineNames[value]}</option>)}</select></div>
        <div><label htmlFor="service">Service</label><select id="service" value={service} onChange={event => { setDebounce(false); setService(event.target.value); }}><option value="">All services</option>{meta.services.map(value => <option key={value} value={value}>{value}</option>)}</select></div>
        <div><label htmlFor="status">HTTP status</label><select id="status" value={status} onChange={event => { setDebounce(false); setStatus(Number(event.target.value)); }}><option value="0">All statuses</option>{meta.statuses.map(value => <option key={value} value={value}>{value}{value >= 500 ? ' · server error' : ''}</option>)}</select></div>
      </div>
      <p className="engine-hint">{engineDescriptions[engine]}{meta.services_truncated ? ' Service options show the first 256 names; all-services queries still cover the full dataset.' : ''}</p>
      <div className="range-heading"><h3>Time window <span>{percent((end - start) / 10)} of dataset span</span></h3><div className="presets" role="group" aria-label="Time window presets">{[1, 10, 100, 1000].map(value => <button key={value} aria-pressed={start === 0 && end === value} onClick={() => { setDebounce(false); setStart(0); setEnd(value); }}>{value / 10}%</button>)}</div></div>
      <div className="range-grid">
        <label htmlFor="range-start">Range start <output aria-hidden="true">{percent(start / 10)}</output><input id="range-start" type="range" min="0" max="999" step="1" value={start} aria-valuetext={`${start / 10} percent of dataset time span`} onChange={event => { const value = Math.min(Number(event.target.value), end - 1); if (value !== start) { setDebounce(true); setStart(value); } }} /></label>
        <label htmlFor="range-end">Range end <output aria-hidden="true">{percent(end / 10)}</output><input id="range-end" type="range" min="1" max="1000" step="1" value={end} aria-valuetext={`${end / 10} percent of dataset time span`} onChange={event => { const value = Math.max(Number(event.target.value), start + 1); if (value !== end) { setDebounce(true); setEnd(value); } }} /></label>
      </div>
      <div className="range-values"><span><b>FROM</b> {timestamp(bounds.from_us)}</span><span><b>TO</b> {bounds.to_us === null ? 'Dataset end · maximum timestamp included' : `${timestamp(bounds.to_us)} · exclusive`}</span></div>
    </section>

    <div className="results-heading"><h2><span className="section-number">02</span>Read the signal</h2><span role="status" className="query-state">{loading ? 'Running query…' : error ? 'Query failed' : `${engineNames[engine]} · ${number(aggregate?.count ?? 0)} matching events`}</span></div>
    {error && <div className="notice error" role="alert"><h3>Query unavailable</h3><p>{error}</p><button onClick={() => { setDebounce(false); setRetry(value => value + 1); }}>Retry query</button></div>}
    <div className={`results ${loading ? 'is-loading' : ''}`} aria-busy={loading}>
      <div className="summary-grid">
        <Summary label="MATCHING EVENTS" value={aggregate ? number(aggregate.count) : '—'} detail="Exact count · no sampling" accent="teal" testId="match-count" />
        <Summary label="SERVER ERRORS" value={aggregate ? number(aggregate.error_count) : '—'} detail={aggregate ? `${percent(aggregate.count ? aggregate.error_count / aggregate.count * 100 : 0)} of matches · status ≥ 500` : 'Status ≥ 500'} accent="amber" testId="error-count" />
        <Summary label="MEAN EVENT DURATION" value={aggregate ? duration(aggregate.mean_duration_us) : '—'} detail="Event latency · not query latency" accent="lime" testId="mean-duration" />
        <Summary label="QUERY WORK AVOIDED" value={stats ? percent(avoided) : '—'} detail={stats ? `${number(stats.rows_skipped)} / ${number(stats.total_rows)} rows skipped` : 'Rows skipped / dataset rows'} accent="teal" testId="work-avoided" />
      </div>
      {data && aggregate?.count === 0 && <div className="notice empty" role="status"><strong>No matching events</strong><p>Try a wider time window or clear the service and status filters. Counts below are exact zeros, not missing data.</p><button onClick={() => { setDebounce(false); setStart(0); setEnd(1000); setService(''); setStatus(0); }}>Reset filters</button></div>}
      <section className="panel chart-panel" aria-labelledby="timeline-title"><div className="section-heading"><div><h2 id="timeline-title">Event timeline</h2><p className="muted">Exact counts across the selected time window</p></div><div className="legend"><span><i className="teal-dot" />Events</span><span><i className="amber-dot" />Errors</span></div></div>{data ? <Timeline data={data.profile.timeline} /> : <ChartPlaceholder loading={loading} />}</section>
      <div className="detail-grid">
        <section className="panel" aria-labelledby="histogram-title"><div className="section-heading"><div><h2 id="histogram-title">Duration distribution</h2><p className="muted">Where event latency accumulates</p></div><span className="micro-label">HISTOGRAM</span></div>{data ? <Histogram data={data.profile.histogram} /> : <ChartPlaceholder loading={loading} />}</section>
        <section className="panel" aria-labelledby="services-title"><div className="section-heading"><div><h2 id="services-title">Services in view</h2><p className="muted">{data?.profile.services_truncated ? 'Top 20 services by matching count' : 'Ranked by matching count'}</p></div></div>
          <div className="table-scroll"><table><caption className="sr-only">Matching events, errors, and mean event duration by service</caption><thead><tr><th scope="col">Service</th><th scope="col">Events</th><th scope="col">Errors</th><th scope="col">Mean</th></tr></thead><tbody>{data?.profile.services.map(item => <tr key={item.service}><th scope="row"><span className="service-dot" />{item.service}</th><td>{number(item.count)}</td><td className={item.error_count ? 'error-text' : ''}>{number(item.error_count)}</td><td>{duration(item.mean_duration_us)}</td></tr>)}</tbody></table>{!data?.profile.services.length && <p className="table-empty">{loading ? 'Loading services…' : data ? 'No services match this query.' : 'No query results.'}</p>}</div>
        </section>
      </div>
      <section className="panel diagnostics" aria-labelledby="diagnostics-title"><div className="section-heading"><div><h2 id="diagnostics-title">Under the hood</h2><p className="muted">Query execution and chart generation are separate server passes.</p></div><span className="engine-badge">{engine}</span></div>
        <div className="diagnostic-grid">
          <div><h3>Query work</h3><dl><Metric label="Rows examined" value={stats ? number(stats.rows_examined) : '—'} testId="rows-examined" /><Metric label="Rows skipped" value={stats ? number(stats.rows_skipped) : '—'} testId="rows-skipped" /><Metric label="Index comparisons" value={stats ? number(stats.index_comparisons) : '—'} /><Metric label="Server query time" value={data ? milliseconds(data.timing.query_ms) : '—'} /></dl></div>
          <div><h3>Chart profile · indexed pass</h3><dl><Metric label="Profile rows examined" value={data ? number(data.profile.rows_examined) : '—'} /><Metric label="Profile index comparisons" value={data ? number(data.profile.index_comparisons) : '—'} /><Metric label="Server chart time" value={data ? milliseconds(data.timing.profile_ms) : '—'} /><Metric label="Combined server work" value={data ? milliseconds(data.timing.server_work_ms) : '—'} /></dl></div>
        </div>
        <p className="footnote">Server timers exclude network transfer and browser rendering. Chart profiling always uses indexed traversal and is not included in query-only work avoided. Unmeasurably short timers are labeled, never inferred as zero.</p>
      </section>
    </div>

    <section className="panel comparison" aria-labelledby="compare-title"><div className="section-heading"><div><h2 id="compare-title"><span className="section-number">03</span>Same question. Three engines.</h2><p className="muted">Compare the current filters without changing the dataset.</p></div><button className="primary-button" onClick={compare} disabled={comparing || loading || !!error}>{comparing ? 'Comparing engines…' : 'Compare engines'}<span aria-hidden="true">↗</span></button></div>
      <p className="comparison-note">Warm batch means, not p95 or end-to-end latency. Each engine runs sequentially for at least 50 ms and 3 iterations, subject to an iteration cap and request timeout. Chart profiling is excluded.</p>
      {comparing && <p role="status">Measuring warm query batches for all three engines…</p>}
      {compareError && <p className="error-text" role="alert">{compareError} Use Compare engines to retry.</p>}
      {comparison ? <>
        <p role="status" className={comparison.equivalent ? 'equivalence' : 'error-text'}>{comparison.equivalent ? '✓ Equivalent aggregates across all three engines' : 'Warning: engine aggregates differ'}</p>
        <div className="table-scroll"><table data-testid="comparison-table"><caption className="sr-only">Warm sequential engine comparison for the current filters</caption><thead><tr><th scope="col">Engine</th><th scope="col">Matches</th><th scope="col">Rows examined</th><th scope="col">Rows skipped</th><th scope="col">Index comparisons</th><th scope="col">Iterations</th><th scope="col">Mean query</th><th scope="col">Batch time</th></tr></thead><tbody>{comparison.results.map(item => <tr key={item.engine}><th scope="row">{engineNames[item.engine as Engine] ?? item.engine}</th><td>{number(item.aggregate.count)}</td><td>{number(item.stats.rows_examined)}</td><td>{number(item.stats.rows_skipped)}</td><td>{number(item.stats.index_comparisons)}</td><td>{number(item.iterations)}</td><td>{!item.stable_window || item.mean_query_ms === null ? 'Below clock resolution / insufficient window' : milliseconds(item.mean_query_ms)}</td><td>{milliseconds(item.measurement_ms)}</td></tr>)}</tbody></table></div>
      </> : !comparing && <div className="compare-empty"><span aria-hidden="true">≋</span><p>One dataset. Identical predicates. Measured execution.<br /><span>Run an explicit comparison to inspect the trade-offs.</span></p></div>}
    </section>
  </>;
}

function Summary({ label, value, detail, accent, testId }: { label: string; value: string; detail: string; accent: string; testId: string }) {
  return <section className={`summary-card accent-${accent}`}><h3>{label}</h3><strong data-testid={testId}>{value}</strong><p>{detail}</p></section>;
}
function Metric({ label, value, testId }: { label: string; value: string; testId?: string }) {
  return <div><dt>{label}</dt><dd data-testid={testId}>{value}</dd></div>;
}
function ChartPlaceholder({ loading }: { loading: boolean }) {
  return <div className="chart-placeholder">{loading ? 'Fetching exact chart aggregates…' : 'Chart data unavailable'}</div>;
}

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>);
