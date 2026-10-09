/**
 * What the demo's agent published: a page and a chart, made up like the rest.
 * The page carries a script on purpose — a toggle between two ranges — to show
 * that what an agent publishes runs, in a frame of its own.
 */

const day = [38, 41, 40, 44, 52, 61, 58, 55, 49, 47, 63, 71, 66, 59, 54, 50, 48, 46, 52, 57, 60, 53, 45, 41];
const week = [44, 47, 52, 49, 58, 61, 55];

function polyline(values: number[], width: number, height: number) {
  const step = width / (values.length - 1);
  return values.map((value, index) => `${(index * step).toFixed(1)},${(height - (value / 100) * height).toFixed(1)}`).join(' ');
}

export const upsChartSvg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 260" font-family="system-ui, sans-serif">
  <rect width="640" height="260" fill="#ffffff"/>
  <text x="24" y="34" font-size="16" font-weight="600" fill="#1f2328">UPS load, last 24 hours</text>
  <text x="24" y="54" font-size="12" fill="#6b7280">Peak 71% at 11:00 · alert threshold 80%</text>
  <g transform="translate(24,72)">
    <line x1="0" y1="${(160 - 0.8 * 160).toFixed(1)}" x2="592" y2="${(160 - 0.8 * 160).toFixed(1)}" stroke="#d97706" stroke-dasharray="4 4"/>
    <line x1="0" y1="160" x2="592" y2="160" stroke="#e5e7eb"/>
    <polyline points="${polyline(day, 592, 160)}" fill="none" stroke="#4f46e5" stroke-width="2.5" stroke-linejoin="round"/>
  </g>
  <text x="24" y="250" font-size="11" fill="#6b7280">00:00</text>
  <text x="600" y="250" font-size="11" fill="#6b7280" text-anchor="end">23:00</text>
</svg>
`;

export const upsPreviewHtml = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>UPS dashboard — preview</title>
<style>
  body { font: 14px/1.5 system-ui, sans-serif; color: #1f2328; margin: 24px; }
  h1 { font-size: 18px; margin: 0 0 4px; }
  p.muted { color: #6b7280; margin: 0 0 20px; }
  .tiles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 20px; }
  .tile { border: 1px solid #e5e7eb; border-radius: 8px; padding: 12px; }
  .tile b { display: block; font-size: 22px; font-variant-numeric: tabular-nums; }
  .tile span { color: #6b7280; font-size: 12px; }
  button { font: inherit; border: 1px solid #d1d5db; background: #f9fafb; border-radius: 6px; padding: 4px 10px; cursor: pointer; }
  button[aria-pressed="true"] { background: #4f46e5; color: white; border-color: #4f46e5; }
  svg { width: 100%; height: auto; margin-top: 12px; }
</style>
</head>
<body>
<h1>UPS dashboard — preview</h1>
<p class="muted">What the Grafana dashboard will show, rendered from last week's data.</p>
<div class="tiles">
  <div class="tile"><span>Load now</span><b>53%</b></div>
  <div class="tile"><span>Battery</span><b>100%</b></div>
  <div class="tile"><span>Runtime left</span><b>41 min</b></div>
</div>
<button id="day" aria-pressed="true">24 hours</button>
<button id="week" aria-pressed="false">7 days</button>
<svg viewBox="0 0 600 180"><line x1="0" y1="36" x2="600" y2="36" stroke="#d97706" stroke-dasharray="4 4"/><polyline id="line" fill="none" stroke="#4f46e5" stroke-width="2.5" points="${polyline(day, 600, 180)}"/></svg>
<script>
  const ranges = { day: '${polyline(day, 600, 180)}', week: '${polyline(week, 600, 180)}' };
  for (const id of Object.keys(ranges)) {
    document.getElementById(id).addEventListener('click', () => {
      document.getElementById('line').setAttribute('points', ranges[id]);
      for (const other of Object.keys(ranges)) document.getElementById(other).setAttribute('aria-pressed', String(other === id));
    });
  }
</script>
</body>
</html>
`;
