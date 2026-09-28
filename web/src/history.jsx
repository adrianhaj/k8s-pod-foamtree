// Snapshot history, kept in the browser: each refresh's /resources/cpu and
// /resources/memory responses, gzipped, tagged with time and context. Plus the
// diff of two snapshots (merged nodes, as mergeResources builds them).

const { workloadKey } = window.k8sWorkload;

// ponytail: 64 MB of gzipped snapshots across all contexts, oldest dropped
// first: ~300 refreshes of a 5 000-pod cluster (~200 KB each). Gone on reload;
// IndexedDB or a server-side recorder when post-mortems need longer windows.
const HISTORY_BYTES = 64e6;

const pipe = (body, stream) => new Response(new Blob([body]).stream().pipeThrough(stream));

// raw is `[cpuResponse,memResponse]`, the two response bodies verbatim.
async function pack(t, context, raw) {
  return { t, context, data: await pipe(raw, new CompressionStream("gzip")).arrayBuffer() };
}

// → [cpuResponse, memResponse], ready for mergeResources.
async function unpack(entry) {
  return JSON.parse(await pipe(entry.data, new DecompressionStream("gzip")).text());
}

function record(history, entry, limit = HISTORY_BYTES) {
  const next = [...history, entry];
  let bytes = next.reduce((s, e) => s + e.data.byteLength, 0);
  while (bytes > limit && next.length > 1) bytes -= next.shift().data.byteLength;
  return next;
}

// Pods match by namespace/name. A workload (namespace + workloadKey) counts as
// resized when its set of per-pod requests changed on either axis, so scaling
// alone is not a resize but a rollout with new requests or an in-place resize is.
function diff(before, after) {
  const index = nodes => {
    const pods = new Map(), workloads = new Map();
    for (const n of nodes) {
      for (const p of n.pods) {
        pods.set(`${p.namespace}/${p.name}`, { pod: p, node: n.name });
        const key = `${p.namespace}/${workloadKey(p.name)}`;
        if (!workloads.has(key)) workloads.set(key, { cpu: new Set(), mem: new Set(), pods: [] });
        const w = workloads.get(key);
        w.cpu.add(p.cpu);
        w.mem.add(p.mem);
        w.pods.push(p);
      }
    }
    return { pods, workloads };
  };
  const a = index(before), b = index(after);
  const added = [...b.pods].filter(([k]) => !a.pods.has(k)).map(([, v]) => v);
  const removed = [...a.pods].filter(([k]) => !b.pods.has(k)).map(([, v]) => v);
  const same = (x, y) => x.size === y.size && [...x].every(v => y.has(v));
  const resized = [];
  for (const [key, w] of b.workloads) {
    const was = a.workloads.get(key);
    if (was && (!same(was.cpu, w.cpu) || !same(was.mem, w.mem))) resized.push({ key, before: was, after: w });
  }

  const deltas = new Map();
  const add = (nodes, sign) => {
    for (const n of nodes) {
      const d = deltas.get(n.name) || { name: n.name, pods: 0, cpu: 0, mem: 0, before: false, after: false };
      d.pods += sign * n.pods.length;
      d.cpu += sign * n.cpuUsed;
      d.mem += sign * n.memUsed;
      d[sign < 0 ? "before" : "after"] = true;
      deltas.set(n.name, d);
    }
  };
  add(before, -1);
  add(after, 1);
  // Float sums of the same pods can differ in the last bits; 1 millicore and
  // 0.01 MiB are below anything the UI prints.
  const nodes = [...deltas.values()].filter(d =>
    !d.before || !d.after || d.pods !== 0 || Math.abs(d.cpu) >= 1 || Math.abs(d.mem) >= 0.01);

  const pods = new Set([...added.map(x => x.pod), ...resized.flatMap(r => r.after.pods)]);
  return { added, removed, resized, nodes, pods };
}

window.k8sHistory = { pack, unpack, record, diff };
