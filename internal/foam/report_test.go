package foam

import (
	"fmt"
	"testing"
)

func TestReport(t *testing.T) {
	cpuLimit, memLimit := int64(500), 1_610_612.736 // 1.5Gi in decimal kB
	nodes := []Node{
		{Name: "b", CPU: 4000, Memory: 8_000_000},
		{Name: "a", CPU: 2000, Memory: 1_000_000, Zone: "z1", Pool: "spot", InstanceType: "m5.large", Unschedulable: true},
	}
	pods := []Pod{
		{Name: "web-2", Namespace: "prod", NodeName: "a", CPU: 250, Memory: 200_000, QOS: "Burstable",
			Containers: []Container{{Name: "web", CPU: 250, Memory: 200_000}}},
		{Name: "api", Namespace: "dev", NodeName: "a", CPU: 400, Memory: 100_000, QOS: "Guaranteed",
			CPULimit: &cpuLimit, MemoryLimit: &memLimit,
			Containers: []Container{{Name: "api", CPU: 400, Memory: 100_000, MemoryLimit: &memLimit}}},
		{Name: "pending", Namespace: "dev", CPU: 100, QOS: "Burstable",
			Containers: []Container{{Name: "job", CPU: 100}}},
		{Name: "orphan", Namespace: "dev", NodeName: "gone"},
	}
	rows := Report(nodes, pods)
	got := ""
	for _, r := range rows {
		got += fmt.Sprintf("%s %s/%s cpu=%d/%s mem=%d/%s %v %v\n", r.Node, r.Namespace, r.Pod,
			r.CPU, ptr(r.CPULimit), r.MemoryBytes, ptr(r.MemoryLimitBytes), r.NodeWarnings, r.Findings)
	}
	want := "a dev/api cpu=400/500 mem=100000000/1610612736 [cordoned] []\n" +
		"a prod/web-2 cpu=250/- mem=200000000/- [cordoned] [missing-limits]\n" +
		"b / cpu=0/- mem=0/- [] []\n" +
		"gone dev/orphan cpu=0/- mem=0/- [] []\n" +
		" dev/pending cpu=100/- mem=0/- [] [missing-requests missing-limits]\n"
	if got != want {
		t.Fatalf("got\n%swant\n%s", got, want)
	}
	if r := rows[4]; r.NodeCPU != 0 || r.Zone != "" || r.QOS != "Burstable" {
		t.Fatalf("pending pod has node columns: %+v", r)
	}
	if r := rows[0]; r.Zone != "z1" || r.Pool != "spot" || r.InstanceType != "m5.large" ||
		r.NodeCPU != 2000 || r.NodeMemoryBytes != 1_000_000_000 || r.QOS != "Guaranteed" {
		t.Fatalf("node columns: %+v", r)
	}
}

func ptr(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}
