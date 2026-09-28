// export.jsx — image and report downloads from the header's Export menu.
//
// The 2D SVG is drawn from the data with the treemap's own squarify layout and
// colours instead of rasterising the page: it stays vector for decks, needs no
// library, and the PNG is that SVG drawn onto a canvas. It mirrors NodeCard and
// PodBox in treemap.jsx, so a change to their look belongs in both.

const { squarify, cardItems } = window.k8sTreemap;
const { worstSeverity, WarnIcon } = window.k8sNodeStatus;
const { worstFindingSeverity } = window.k8sPodAudit;
const { hsl, token } = window.k8sScene3D;
const { workloadKey } = window.k8sWorkload;

const MONO = '"JetBrains Mono", ui-monospace, "SF Mono", monospace';
const SCALE = 2;

// Hex plus fill-opacity rather than hsla(): not every SVG consumer reads CSS colours.
const hex = rgb => "#" + rgb.map(v => Math.round(v * 255).toString(16).padStart(2, "0")).join("");
const paint = (h, s, l, a = 1) => ({ fill: hex(hsl(h, s, l)), fillOpacity: a });
const stroke = (h, s, l, a = 1) => ({ stroke: hex(hsl(h, s, l)), strokeOpacity: a });
const utilColor = u => (u > 0.85 ? "#ef4444" : u > 0.6 ? "#f59e0b" : u > 0.3 ? "#10b981" : "#3b82f6");

// SVG text does not ellipsize; monospace glyphs are ~0.6em wide.
function fit(text, px, width) {
  const n = Math.floor(width / (px * 0.6));
  return text.length <= n ? text : n > 1 ? text.slice(0, n - 1) + "…" : "";
}

// .pod-empty and its warn-* variants: 45deg stripes, border, label alpha.
const EMPTY = {
  none: { rgb: "#ffffff", a: [0.04, 0.08], period: 8, border: 0.12, text: 0.45 },
  danger: { rgb: "#ef4444", a: [0.1, 0.28], period: 10, border: 0.55, text: 0.7 },
  warn: { rgb: "#f59e0b", a: [0.1, 0.26], period: 10, border: 0.5, text: 0.7 },
  info: { rgb: "#60a5fa", a: [0.08, 0.2], period: 10, border: 0.45, text: 0.65 },
};

function TreemapSVG({ nodes, width, height, metric, hueOf, nodeStyle, density, showLabels, match, highlight, highlightActive }) {
  const pad = 14; // .grid-wrap padding
  const slots = squarify(
    nodes.map((node, idx) => ({ node, idx, value: metric === "cpu" ? node.cpuCapacity : node.memCapacity })),
    pad, pad, width - pad * 2, height - pad * 2
  );
  const look = {
    queryActive: !!(match && match.active), match, highlight, pinned: highlightActive,
    accent: token("--accent", "#7c5cff"),
    sev: { danger: token("--danger", "#ef4444"), warn: token("--warn", "#f59e0b"), info: token("--info", "#60a5fa") },
  };
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width={width} height={height} viewBox={`0 0 ${width} ${height}`} fontFamily={MONO}>
      <defs>
        {Object.entries(EMPTY).map(([k, e]) => (
          <pattern key={k} id={`hatch-${k}`} width={e.period} height={e.period} patternUnits="userSpaceOnUse" patternTransform="rotate(-45)">
            <rect width={e.period / 2} height={e.period} fill={e.rgb} fillOpacity={e.a[0]} />
            <rect x={e.period / 2} width={e.period / 2} height={e.period} fill={e.rgb} fillOpacity={e.a[1]} />
          </pattern>
        ))}
        <linearGradient id="node-header" x2="0" y2="1">
          <stop offset="0" stopColor="#000" stopOpacity=".15" />
          <stop offset="1" stopColor="#000" stopOpacity="0" />
        </linearGradient>
        {nodeStyle === "gradient" && slots.map(s => (
          // ponytail: CSS 135deg vs the bounding-box diagonal differ slightly on non-square cards.
          <linearGradient key={s.idx} id={`card-${s.idx}`} x2="1" y2="1">
            <stop offset="0" stopColor={hex(hsl(hueOf(s.idx), 60, 22))} />
            <stop offset="1" stopColor={hex(hsl(hueOf(s.idx), 50, 12))} />
          </linearGradient>
        ))}
      </defs>
      <rect width={width} height={height} fill={token("--bg", "#07080c")} />
      {slots.map(s => (
        <SvgCard key={s.node.id} slot={s} hue={hueOf(s.idx)} metric={metric} nodeStyle={nodeStyle}
          density={density} showLabels={showLabels} look={look} />
      ))}
    </svg>
  );
}

function SvgCard({ slot, hue, metric, nodeStyle, density, showLabels, look }) {
  const { node } = slot;
  const bw = nodeStyle === "outlined" ? 1.5 : 1;
  const w = slot.w - 6, h = slot.h - 6;
  const padding = density === "compact" ? 4 : 6;
  const headerH = density === "compact" ? 26 : 32;
  const nodeDim = look.queryActive && look.match.dimNodes.has(node.name);
  const podMatched = pod => look.queryActive && look.match.pods.has(pod);
  const { items, cap, used, empty } = cardItems(node, metric, podMatched);
  const innerW = w - bw * 2 - padding * 2, innerH = h - bw * 2 - headerH - padding;
  const laid = innerW > 0 && innerH > 0 ? squarify(items, padding, headerH, innerW, innerH) : [];
  const warnSev = worstSeverity(node.warnings);
  const util = used / cap;
  const utilText = `${Math.round(util * 100)}%`;

  const bg = nodeStyle === "solid" ? paint(hue, 55, 24)
    : nodeStyle === "gradient" ? { fill: `url(#card-${slot.idx})` }
    : paint(hue, 20, 12);
  const border = nodeStyle === "outlined" ? stroke(hue, 70, 55) : stroke(hue, 40, 45, 0.4);
  const podBg = nodeStyle === "solid" ? paint(hue, 65, 38, 0.65)
    : nodeStyle === "gradient" ? paint(hue, 55, 32, 0.9)
    : paint(hue, 45, 25, 0.7);

  const inner = w - bw * 2;
  const utilX = inner - 10;
  const badgeX = utilX - utilText.length * 6.6 - 6 - 11;
  const nameEnd = (warnSev ? badgeX : utilX - utilText.length * 6.6) - 8;
  const e = EMPTY[warnSev || "none"];

  return (
    <g opacity={nodeDim ? 0.32 : 1}>
      <rect x={slot.x + bw / 2} y={slot.y + bw / 2} width={w - bw} height={h - bw} rx={10} {...bg} {...border} strokeWidth={bw} />
      <g transform={`translate(${slot.x + bw} ${slot.y + bw})`}>
        <rect width={inner} height={headerH} fill="url(#node-header)" />
        <circle cx={13.5} cy={headerH / 2} r={3.5} {...paint(hue, 80, 65)} />
        <text x={25} y={headerH / 2} dominantBaseline="central" fontSize={11} fontWeight={500} fill="#fff" fillOpacity={0.95}>
          {fit(node.name, 11, nameEnd - 25)}
        </text>
        {warnSev && (
          <g transform={`translate(${badgeX} ${headerH / 2 - 5.5})`} color={look.sev[warnSev]}><WarnIcon size={11} /></g>
        )}
        <text x={utilX} y={headerH / 2} dominantBaseline="central" textAnchor="end" fontSize={11} fontWeight={600} fill={utilColor(util)}>
          {utilText}
        </text>
        {laid.map((it, i) => it.empty ? (
          <g key={`empty-${i}`}>
            <rect x={it.x} y={it.y} width={Math.max(0, it.w - 2)} height={Math.max(0, it.h - 2)} rx={6}
              fill={`url(#hatch-${warnSev || "none"})`} stroke={e.rgb} strokeOpacity={e.border}
              strokeDasharray={warnSev ? null : "3 3"} />
            {it.w > 60 && it.h > 30 && (
              <text x={it.x + it.w / 2 - 1} y={it.y + it.h / 2 - 1} textAnchor="middle" dominantBaseline="central"
                fontSize={10} fill="#fff" fillOpacity={e.text}>idle · {Math.round((empty / cap) * 100)}%</text>
            )}
          </g>
        ) : (
          <SvgPod key={`pod-${i}`} it={it} hue={hue} metric={metric} showLabels={showLabels} podBg={podBg} look={look}
            matched={podMatched(it.pod)} dim={look.queryActive && !nodeDim && !podMatched(it.pod)} />
        ))}
      </g>
    </g>
  );
}

// PodBox's states: query dim wins over the workload highlight, which wins over a match.
function SvgPod({ it, hue, metric, showLabels, podBg, look, matched, dim }) {
  const { pod, x, y, w, h } = it;
  const peer = !!look.highlight && workloadKey(pod.name) === look.highlight;
  const opacity = dim ? 0.16 : look.highlight && !peer ? (look.pinned ? 0.22 : 0.55) : 1;
  const ring = !dim && (matched || peer);
  const headerH = h > 28 ? 12 : 0;
  const containers = squarify(
    pod.containers.map(c => ({ container: c, value: metric === "cpu" ? c.cpu : c.mem })),
    3, headerH + 3, Math.max(0, w - 4), Math.max(0, h - headerH - 4)
  );
  const sev = w > 24 && h > 16 && worstFindingSeverity(pod.findings);
  return (
    <g opacity={opacity}>
      <rect x={x} y={y} width={Math.max(0, w - 2)} height={Math.max(0, h - 2)} rx={4} {...podBg}
        {...(ring ? { stroke: look.accent, strokeWidth: 1.5 } : { ...stroke(hue, 60, 55, 0.5), strokeWidth: 1 })} />
      {headerH > 0 && showLabels && w > 50 && (
        <text x={x + 5} y={y + 7} dominantBaseline="central" fontSize={8} fill="#fff" fillOpacity={0.85}>
          {fit(pod.shortName, 8, w - 10)}
        </text>
      )}
      {sev && <g transform={`translate(${x + w - 14} ${y + 2})`} color={look.sev[sev]}><WarnIcon size={9} /></g>}
      {containers.map((c, i) => (
        <g key={i}>
          <rect x={x + c.x} y={y + c.y} width={Math.max(0, c.w - 1)} height={Math.max(0, c.h - 1)} rx={2}
            {...(c.container.init ? paint(hue, 10, 55, 0.85) : paint(hue, 70, 68, 0.92))} />
          {showLabels && c.w > 40 && c.h > 18 && (
            <text x={x + c.x + c.w / 2} y={y + c.y + c.h / 2} textAnchor="middle" dominantBaseline="central"
              fontSize={9} fontWeight={500} fill="#000" fillOpacity={0.85}>{fit(c.container.name, 9, c.w - 6)}</text>
          )}
        </g>
      ))}
    </g>
  );
}

function treemapSVG(props) {
  const host = document.createElement("div");
  const root = ReactDOM.createRoot(host);
  ReactDOM.flushSync(() => root.render(<TreemapSVG {...props} />));
  const svg = new XMLSerializer().serializeToString(host.firstChild);
  root.unmount();
  return svg;
}

const svgURL = svg => "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);

// A data: URL, since the CSP's img-src allows data: but not blob:.
async function svgToPNG(svg, width, height) {
  const img = new Image();
  img.src = svgURL(svg);
  await img.decode();
  const c = document.createElement("canvas");
  c.width = width * SCALE;
  c.height = height * SCALE;
  c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
  return c;
}

const toBlob = canvas => new Promise(resolve => canvas.toBlob(resolve, "image/png"));

function download(blob, name) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// PDF through the browser's print dialog ("Save as PDF"): print CSS hides the
// app and shows only this image, so a live DOM re-flowing to paper size never
// comes into it. The 2D SVG stays vector in the PDF. Removed on afterprint,
// since print() does not block in every browser.
async function printImage(src) {
  const img = document.createElement("img");
  img.className = "print-sheet";
  img.src = src;
  await img.decode();
  document.body.append(img);
  addEventListener("afterprint", () => img.remove(), { once: true });
  print();
}

// k8sfoams-<context>-<view>-2026-09-28-1405
function fileName(context, view) {
  const d = new Date(), p = n => String(n).padStart(2, "0");
  const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}`;
  return ["k8sfoams", (context || "cluster").replace(/[^\w.-]+/g, "_"), view, stamp].filter(Boolean).join("-");
}

// A native <details> dropdown: no open state to keep, and Escape/outside
// clicks are not worth code here.
function ExportMenu({ view, context, treemap, gridRef, sceneRef }) {
  const ref = React.useRef(null);
  const q = context ? `?context=${encodeURIComponent(context)}` : "";
  const close = () => { ref.current.open = false; };
  const run = fn => () => { close(); fn(); };
  const svg2d = () => {
    const { width, height } = gridRef.current.getBoundingClientRect();
    const w = Math.round(width), h = Math.round(height);
    return { svg: treemapSVG({ ...treemap, width: w, height: h }), w, h };
  };
  const items = view === "3d" ? [
    // Without WebGL there is no scene to snapshot, only the notice.
    ["PNG image", run(async () => sceneRef.current &&
      download(await toBlob(sceneRef.current(SCALE)), `${fileName(context, "3d")}.png`))],
    ["PDF (print)", run(() => sceneRef.current && printImage(sceneRef.current(SCALE).toDataURL("image/png")))],
  ] : [
    ["SVG image", run(() => download(new Blob([svg2d().svg], { type: "image/svg+xml" }), `${fileName(context, "2d")}.svg`))],
    ["PNG image", run(async () => {
      const { svg, w, h } = svg2d();
      download(await toBlob(await svgToPNG(svg, w, h)), `${fileName(context, "2d")}.png`);
    })],
    ["PDF (print)", run(() => printImage(svgURL(svg2d().svg)))],
  ];
  return (
    <details className="export-menu" ref={ref}>
      <summary className="icon-btn" title="Export">
        <svg viewBox="0 0 16 16" width="14" height="14">
          <path d="M8 2 V10 M4.5 6.5 L8 10 L11.5 6.5 M3 13.5 H13" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </summary>
      <div className="export-pop">
        {items.map(([label, onClick]) => <button key={label} onClick={onClick}>{label}</button>)}
        <a href={`/report.csv${q}`} download={`${fileName(context, "")}.csv`} onClick={close}>CSV report</a>
        <a href={`/report.json${q}`} download={`${fileName(context, "")}.json`} onClick={close}>JSON report</a>
      </div>
    </details>
  );
}

window.k8sExport = { ExportMenu };
