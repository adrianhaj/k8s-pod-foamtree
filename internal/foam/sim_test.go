package foam

import (
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func roomy(name string) Node {
	return Node{Name: name, CPU: 4000, Memory: 8_000_000, AllocCPU: 4000, AllocMemory: 8_000_000, AllocPods: 110}
}

func reasonsOf(verdicts []Verdict) map[string][]string {
	out := map[string][]string{}
	for _, v := range verdicts {
		out[v.Node] = v.Reasons
	}
	return out
}

func TestFitFilters(t *testing.T) {
	busy, cordoned, spot, soft, full := roomy("busy"), roomy("cordoned"), roomy("spot"), roomy("soft"), roomy("full")
	cordoned.Unschedulable = true
	spot.Taints = []Taint{{Key: "spot", Value: "true", Effect: "NoSchedule"}}
	soft.Taints = []Taint{{Key: "spot", Value: "true", Effect: "PreferNoSchedule"}}
	full.AllocPods = 1
	running := []Pod{
		{Name: "a", NodeName: "busy", CPU: 3000, Memory: 1_000_000},
		{Name: "b", NodeName: "full"},
		{Name: "pending"},
	}
	got := reasonsOf(Fit([]Node{roomy("ok"), busy, cordoned, spot, soft, full}, running, Pod{CPU: 2000, Memory: 1_000_000}))
	want := map[string][]string{
		"ok":       {},
		"busy":     {"insufficient cpu: requires 2000m, available 1000m"},
		"cordoned": {"cordoned"},
		"spot":     {"untolerated taint: spot=true:NoSchedule"},
		"soft":     {},
		"full":     {"too many pods: 1 allocatable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestFitReportsEveryFailingFilter(t *testing.T) {
	n := roomy("n")
	n.Unschedulable = true
	n.AllocMemory = 3_435_973.837 // 3.2 Gi, in kB
	p := Pod{CPU: 5000, Memory: 17_179_869.184, NodeSelector: map[string]string{"disk": "ssd"}}
	got := Fit([]Node{n}, nil, p)[0].Reasons
	want := []string{"cordoned", "node selector mismatch", "insufficient cpu: requires 5000m, available 4000m",
		"insufficient memory: requires 16.0Gi, available 3.2Gi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestFitTolerationsAndSelectors(t *testing.T) {
	cordoned, spot, ssd := roomy("cordoned"), roomy("spot"), roomy("ssd")
	cordoned.Unschedulable = true
	spot.Taints = []Taint{{Key: "spot", Value: "true", Effect: "NoExecute"}}
	ssd.Labels = map[string]string{"disk": "ssd"}
	p := Pod{Tolerations: []corev1.Toleration{
		{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpExists},
		{Key: "spot", Operator: corev1.TolerationOpEqual, Value: "true"},
	}}
	if got := reasonsOf(Fit([]Node{cordoned, spot}, nil, p)); len(got["cordoned"])+len(got["spot"]) != 0 {
		t.Fatalf("tolerated taints rejected: %v", got)
	}
	p = Pod{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{
			{Key: "disk", Operator: corev1.NodeSelectorOpIn, Values: []string{"ssd", "nvme"}}}}}}}}}
	got := reasonsOf(Fit([]Node{roomy("plain"), ssd}, nil, p))
	if fmt.Sprint(got["plain"], got["ssd"]) != "[node selector mismatch] []" {
		t.Fatalf("affinity: %v", got)
	}
}

// A BestEffort pod requests nothing, and the scheduler skips a resource the pod does not ask for.
func TestFitZeroRequestSkipsResourceCheck(t *testing.T) {
	n := roomy("n")
	if got := Fit([]Node{n}, []Pod{{NodeName: "n", CPU: 5000, Memory: 9_000_000}}, Pod{})[0].Reasons; len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

// Below 1Gi the reason must switch to Mi, not print a useless "0.0Gi".
func TestFitMemoryReasonUsesMiBelowOneGi(t *testing.T) {
	n := roomy("n")
	n.AllocMemory = 500_000                                     // ~476.8Mi
	got := Fit([]Node{n}, nil, Pod{Memory: 900_000})[0].Reasons // ~858.3Mi requested
	want := []string{"insufficient memory: requires 858.3Mi, available 476.8Mi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestFitIsSortedByNode(t *testing.T) {
	got := Fit([]Node{roomy("b"), roomy("a")}, nil, Pod{})
	if got[0].Node != "a" || got[1].Node != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestHypothetical(t *testing.T) {
	p, err := Hypothetical("500m", "1Gi", "disk=ssd,zone=a", "spot=true:NoSchedule, gpu:NoExecute,dedicated")
	if err != nil {
		t.Fatal(err)
	}
	want := []corev1.Toleration{
		{Key: "spot", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
		{Key: "gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		{Key: "dedicated", Operator: corev1.TolerationOpExists},
	}
	if p.CPU != 500 || p.Memory != 1_073_741.824 || fmt.Sprint(p.NodeSelector) != "map[disk:ssd zone:a]" || !reflect.DeepEqual(p.Tolerations, want) {
		t.Fatalf("got %+v", p)
	}
	if p, err := Hypothetical("", "", "", ""); err != nil || p.CPU != 0 || p.Memory != 0 || len(p.NodeSelector)+len(p.Tolerations) != 0 {
		t.Fatalf("empty form: %+v %v", p, err)
	}
	bad := [][4]string{{"lots", "", "", ""}, {"", "1Gb", "", ""}, {"-1", "", "", ""}, {"", "", "disk", ""},
		{"", "", "", ":NoSchedule"}, {"", "", "", "spot:Never"}, {"1e30", "", "", ""}, {"", "1e30", "", ""}}
	for _, b := range bad {
		if _, err := Hypothetical(b[0], b[1], b[2], b[3]); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestDrain(t *testing.T) {
	a, b, c, spot := roomy("a"), roomy("b"), roomy("c"), roomy("spot")
	a.AllocCPU, b.AllocCPU = 2000, 3000
	spot.Taints = []Taint{{Key: "spot", Effect: "NoSchedule"}}
	on := func(name, ctrl string, cpu int64) Pod {
		return Pod{Name: name, Namespace: "ns", NodeName: "c", Controller: ctrl, CPU: cpu, Memory: 1_000_000}
	}
	pods := []Pod{
		{Name: "resident", Namespace: "ns", NodeName: "b", CPU: 500},
		on("small", "ReplicaSet", 500), on("big", "StatefulSet", 2500), on("huge", "ReplicaSet", 3500),
		on("agent", "DaemonSet", 100), on("static", "Node", 100), on("bare", "", 100),
	}
	got, ok := Drain([]Node{a, b, c, spot}, pods, "c")
	if !ok {
		t.Fatal("node not found")
	}
	// Largest first onto the least-allocated node: big takes b (2500 of 2500 free), small then fits a.
	want := DrainResult{
		Moved: []Placement{{Pod: "ns/big", Node: "b"}, {Pod: "ns/small", Node: "a"}},
		Pending: []Placement{{Pod: "ns/huge",
			Reason: "0/3 nodes are available: 2 insufficient cpu, 1 untolerated taint"}},
		Ignored:   []string{"ns/agent", "ns/static"},
		Unmanaged: []string{"ns/bare"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if _, ok := Drain([]Node{a}, nil, "gone"); ok {
		t.Fatal("unknown node reported as drained")
	}
}

func TestDrainEmptyNodeHasEmptyLists(t *testing.T) {
	got, _ := Drain([]Node{roomy("a")}, nil, "a")
	if got.Moved == nil || got.Pending == nil || got.Ignored == nil || got.Unmanaged == nil {
		t.Fatalf("null lists break the UI: %+v", got)
	}
}

func TestDrainLastNode(t *testing.T) {
	got, _ := Drain([]Node{roomy("a")}, []Pod{{Name: "web", Namespace: "ns", NodeName: "a", Controller: "ReplicaSet"}}, "a")
	if fmt.Sprint(got.Pending) != "[{ns/web  0/0 nodes are available}]" {
		t.Fatalf("got %+v", got.Pending)
	}
}
