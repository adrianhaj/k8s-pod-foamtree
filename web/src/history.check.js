// Check for history.jsx. Go tests never run JS: paste this into the devtools
// console of a running dashboard (any --synthetic size); it resolves to "ok".
(async () => {
  const H = window.k8sHistory;
  const pod = (name, cpu, mem) => ({ name, namespace: "ns", cpu, mem });
  const node = (name, pods) => ({
    name, pods,
    cpuUsed: pods.reduce((s, p) => s + p.cpu, 0),
    memUsed: pods.reduce((s, p) => s + p.mem, 0),
  });
  const before = [
    node("n1", [pod("web-7d4b9c8f5d-x2vpq", 100, 64), pod("db-0", 500, 1024)]),
    node("n2", [pod("cache-0", 200, 256)]),
  ];
  const after = [
    node("n1", [pod("web-7d4b9c8f5d-x2vpq", 100, 64), pod("web-7d4b9c8f5d-q8wzt", 100, 64), pod("db-0", 1000, 1024)]),
    node("n3", []),
  ];
  const d = H.diff(before, after);
  const got = {
    added: d.added.map(x => `${x.node}:${x.pod.name}`),
    removed: d.removed.map(x => `${x.node}:${x.pod.name}`),
    resized: d.resized.map(r => r.key),
    nodes: d.nodes.map(n => `${n.name} ${n.pods} ${n.cpu} ${n.mem} ${n.before} ${n.after}`),
    highlighted: [...d.pods].map(p => p.name).sort(),
    unchanged: H.diff(before, before).nodes.length + H.diff(before, before).pods.size,
  };
  const want = {
    added: ["n1:web-7d4b9c8f5d-q8wzt"],
    removed: ["n2:cache-0"],
    resized: ["ns/db"],
    nodes: ["n1 1 600 64 true true", "n2 -1 -200 -256 true false", "n3 0 0 0 false true"],
    highlighted: ["db-0", "web-7d4b9c8f5d-q8wzt"],
    unchanged: 0,
  };
  if (JSON.stringify(got) !== JSON.stringify(want)) return { FAIL: got };

  const e = await H.pack(1, "ctx", '[{"groups":[]},{"groups":[1]}]');
  const round = JSON.stringify(await H.unpack(e));
  if (round !== '[{"groups":[]},{"groups":[1]}]') return { FAIL: round };
  const size = e.data.byteLength;
  const kept = H.record(H.record(H.record([], e, size * 2), { ...e, t: 2 }, size * 2), { ...e, t: 3 }, size * 2);
  if (kept.map(x => x.t).join() !== "2,3") return { FAIL: kept.map(x => x.t) };
  if (H.record([], e, 1).length !== 1) return { FAIL: "record dropped the only entry" };
  return "ok";
})()
