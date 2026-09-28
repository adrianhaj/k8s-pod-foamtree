package foam

import (
	"cmp"
	"math"
	"slices"
)

// ReportRow is one pod with its node and both axes side by side, the flat
// shape a spreadsheet wants. A node without pods gets one row with the pod
// columns empty, so its free capacity still shows. Memory is in bytes here:
// the report is read by people, not by the treemap.
type ReportRow struct {
	Node             string   `json:"node"`
	Zone             string   `json:"zone"`
	Pool             string   `json:"pool"`
	InstanceType     string   `json:"instanceType"`
	NodeCPU          int64    `json:"nodeCpu"`
	NodeMemoryBytes  int64    `json:"nodeMemoryBytes"`
	NodeWarnings     []string `json:"nodeWarnings"`
	Namespace        string   `json:"namespace"`
	Pod              string   `json:"pod"`
	QOS              string   `json:"qos"`
	CPU              int64    `json:"cpu"`
	CPULimit         *int64   `json:"cpuLimit"`
	MemoryBytes      int64    `json:"memoryBytes"`
	MemoryLimitBytes *int64   `json:"memoryLimitBytes"`
	Findings         []string `json:"findings"`
}

func bytes(kB float64) int64 { return int64(math.Round(kB * 1000)) }

// Report lists nodes by name with their pods by namespace then name, like the
// treemap, then the pods the treemap cannot draw: pending ones and any whose
// node is gone. Those keep their nodeName (empty while pending) and have no
// node columns or warnings, only the findings that need no node.
func Report(nodes []Node, pods []Pod) []ReportRow {
	byNode := map[string][]Pod{}
	for _, p := range pods {
		byNode[p.NodeName] = append(byNode[p.NodeName], p)
	}
	nodes = slices.SortedFunc(slices.Values(nodes), func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })
	rows := []ReportRow{}
	for _, n := range nodes {
		base := ReportRow{
			Node: n.Name, Zone: n.Zone, Pool: n.Pool, InstanceType: n.InstanceType,
			NodeCPU: n.CPU, NodeMemoryBytes: bytes(n.Memory), NodeWarnings: Warnings(n), Findings: []string{},
		}
		if len(byNode[n.Name]) == 0 {
			rows = append(rows, base)
		}
		rows = appendPods(rows, base, n, byNode[n.Name])
		delete(byNode, n.Name)
	}
	var unplaced []Pod
	for _, ps := range byNode {
		unplaced = append(unplaced, ps...)
	}
	for _, p := range sortPods(unplaced) {
		rows = appendPods(rows, ReportRow{Node: p.NodeName, NodeWarnings: []string{}}, Node{}, []Pod{p})
	}
	return rows
}

func sortPods(ps []Pod) []Pod {
	slices.SortFunc(ps, func(a, b Pod) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return ps
}

// n is the zero Node for a pod on no known node: Findings then reports only
// what needs no node (missing requests / limits).
func appendPods(rows []ReportRow, base ReportRow, n Node, pods []Pod) []ReportRow {
	for _, p := range sortPods(pods) {
		r := base
		r.Namespace, r.Pod, r.QOS = p.Namespace, p.Name, p.QOS
		r.CPU, r.CPULimit, r.MemoryBytes = p.CPU, p.CPULimit, bytes(p.Memory)
		if p.MemoryLimit != nil {
			v := bytes(*p.MemoryLimit)
			r.MemoryLimitBytes = &v
		}
		r.Findings = Findings(p, n)
		rows = append(rows, r)
	}
	return rows
}
