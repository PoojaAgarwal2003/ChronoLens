import { number, timestamp, type QueryResponse } from './api';

export function Timeline({ data }: { data: QueryResponse['profile']['timeline'] }) {
  const ceiling = Math.max(1, ...data.map(bin => bin.count));
  const count = data.reduce((sum, bin) => sum + bin.count, 0);
  const errors = data.reduce((sum, bin) => sum + bin.error_count, 0);
  const width = 920;
  const height = 176;
  const step = width / Math.max(1, data.length);
  return <figure className="timeline">
    <svg viewBox={`-45 0 ${width + 45} ${height + 20}`} role="img" aria-label={`Event timeline: ${number(count)} matches and ${number(errors)} errors in ${data.length} exact time buckets.`}>
      {[0, 0.5, 1].map(ratio => <g key={ratio}>
        <line x1="0" y1={height * ratio} x2={width} y2={height * ratio} className="gridline" />
        <text x="-10" y={Math.max(12, height * ratio)} textAnchor="end" className="axis-label">{number(Math.round(ceiling * (1 - ratio)))}</text>
      </g>)}
      {data.map((bin, index) => <g key={`${bin.from_us}-${index}`}>
        <title>{timestamp(bin.from_us)} to {timestamp(bin.to_us)} (exclusive): {number(bin.count)} events, {number(bin.error_count)} errors</title>
        <rect x={index * step} y={height - bin.count / ceiling * height} width={Math.max(0.5, step - 2)} height={bin.count / ceiling * height} rx="1" className="event-bar" />
        <rect x={index * step} y={height - bin.error_count / ceiling * height} width={Math.max(0.5, step - 2)} height={bin.error_count / ceiling * height} rx="1" className="error-bar" />
      </g>)}
    </svg>
    <figcaption>
      <span>{data[0] ? timestamp(data[0].from_us) : 'No time buckets'}</span>
      <span>{data.at(-1) ? `${timestamp(data.at(-1)!.to_us)} · exclusive` : ''}</span>
    </figcaption>
    <details className="chart-data"><summary>View timeline values</summary>
      <div className="table-scroll"><table><caption>Exact timeline bucket values; end timestamps are exclusive.</caption><thead><tr><th scope="col">From (µs)</th><th scope="col">To (µs)</th><th scope="col">Events</th><th scope="col">Errors</th></tr></thead>
        <tbody>{data.map((bin, index) => <tr key={index}><td>{bin.from_us}</td><td>{bin.to_us}</td><td>{number(bin.count)}</td><td>{number(bin.error_count)}</td></tr>)}</tbody>
      </table></div>
    </details>
  </figure>;
}

export function Histogram({ data }: { data: QueryResponse['profile']['histogram'] }) {
  const ceiling = Math.max(1, ...data.map(bin => bin.count));
  return <figure className="histogram">
    <ul aria-label="Duration distribution">
      {data.map(bin => <li key={bin.label}>
        <span className="bin-label">{bin.label}</span>
        <span className="bar-track" aria-hidden="true"><span style={{ width: `${bin.count / ceiling * 100}%` }} /></span>
        <span className="bin-count">{number(bin.count)}</span>
      </li>)}
    </ul>
    <figcaption>Fixed duration bins · all matching events</figcaption>
  </figure>;
}
