// Scheduling simulators. The server runs the scheduler maths on its cache;
// this file only asks and shows the answer.

const { useState } = React;

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
  return r.json();
}

// Rejected nodes dim, pods on the nodes that fit light up — the same match
// shape the query bar produces, so 2D and 3D need no changes.
function fitMatch(nodes, verdicts) {
  const dimNodes = new Set(verdicts.filter(v => v.reasons.length > 0).map(v => v.node));
  const pods = new Set();
  let total = 0;
  for (const n of nodes) {
    total += n.pods.length;
    if (!dimNodes.has(n.name)) n.pods.forEach(p => pods.add(p));
  }
  return { active: true, pods, dimNodes, count: pods.size, total, errors: [] };
}

// ponytail: the verdict is a snapshot; a refresh does not re-run it.
function FitPanel({ context, result, onResult }) {
  const [form, setForm] = useState({ cpu: "500m", memory: "1Gi", nodeSelector: "", tolerations: "" });
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  const check = async (e) => {
    e.preventDefault();
    setBusy(true);
    try {
      onResult(await getJSON(`/api/fit?${new URLSearchParams({ ...form, context })}`));
      setError(null);
    } catch (err) {
      onResult(null);
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const field = (key, label, placeholder) => (
    <label className="sim-field">
      <span>{label}</span>
      <input value={form[key]} placeholder={placeholder} spellCheck="false"
        onChange={e => setForm({ ...form, [key]: e.target.value })} />
    </label>
  );

  // Why nodes said no, by reason kind, most common first.
  const kinds = new Map();
  for (const v of result || []) {
    for (const r of v.reasons) {
      const kind = r.split(":")[0];
      kinds.set(kind, (kinds.get(kind) || 0) + 1);
    }
  }
  const fits = (result || []).filter(v => v.reasons.length === 0).length;

  return (
    <div className="sidebar-section">
      <div className="section-label">Can I fit this pod?</div>
      <form className="sim-form" onSubmit={check}>
        {field("cpu", "CPU request", "500m")}
        {field("memory", "Memory request", "1Gi")}
        {field("nodeSelector", "Node selector", "disk=ssd,zone=a")}
        {field("tolerations", "Tolerations", "spot=true:NoSchedule")}
        <button className="btn-primary" type="submit" disabled={busy}>Check</button>
      </form>
      {error && <div className="sim-error">{error}</div>}
      {result && (
        <div className="health-rows">
          <div className="health-row">
            <span className={`audit-swatch ${fits ? "sim-ok" : "sev-danger"}`} />
            <span className="health-name">{fits} / {result.length} nodes fit</span>
            <button className="search-clear" onClick={() => onResult(null)} title="Clear">×</button>
          </div>
          {[...kinds].sort((a, b) => b[1] - a[1]).map(([kind, count]) => (
            <div key={kind} className="health-row">
              <span className="audit-swatch sev-danger" />
              <span className="health-name">{kind}</span>
              <span className="health-count">{count}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// The focused node's verdict with numbers, e.g. "insufficient cpu: requires 4000m, available 1200m".
function FitVerdict({ reasons }) {
  return (
    <div className="overlay-sched">
      <div className="ov-section-title">Can I fit this pod?</div>
      {reasons.length === 0
        ? <span className="status-pill status-ready">fits</span>
        : reasons.map(r => <div key={r} className="sim-error">{r}</div>)}
    </div>
  );
}

window.k8sSimulate = { fitMatch, FitPanel, FitVerdict };
