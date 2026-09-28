// Node topology grouping shared by the 2D map and the 3D scene. Pure data:
// the keys are the node fields the backend fills from well-known labels.

const GROUP_BY = [
  { id: "none", label: "None" },
  { id: "zone", label: "Zone", missing: "no zone" },
  { id: "region", label: "Region", missing: "no region" },
  { id: "pool", label: "Pool", missing: "no pool" },
  { id: "instanceType", label: "Type", missing: "no instance type" },
  { id: "capacityType", label: "Capacity", missing: "no capacity type" },
];

// [{ key, members: [{ node, idx }] }] sorted by key; "none" is one group
// with an empty key. idx is the node's index in `nodes`, which picks its hue.
function groupNodes(nodes, groupBy) {
  const opt = GROUP_BY.find(g => g.id === groupBy) || GROUP_BY[0];
  const groups = new Map();
  nodes.forEach((node, idx) => {
    const key = opt.id === "none" ? "" : node[opt.id] || opt.missing;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push({ node, idx });
  });
  return [...groups].sort(([a], [b]) => a.localeCompare(b)).map(([key, members]) => ({ key, members }));
}

// Requested share of the group's capacity per resource — what shows a zone
// or pool running hotter than its siblings.
function groupUsage(members) {
  const t = { cpuUsed: 0, cpuCap: 0, memUsed: 0, memCap: 0 };
  for (const { node } of members) {
    t.cpuUsed += node.cpuUsed;
    t.cpuCap += node.cpuCapacity;
    t.memUsed += node.memUsed;
    t.memCap += node.memCapacity;
  }
  return { cpu: t.cpuUsed / (t.cpuCap || 1), mem: t.memUsed / (t.memCap || 1) };
}

window.k8sTopology = { GROUP_BY, groupNodes, groupUsage };
