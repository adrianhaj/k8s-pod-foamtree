package foam

import (
	"encoding/json"
	"fmt"
	"testing"
)

var minikube = Node{Name: "minikube", CPU: 2000, Memory: 1_000_000}

func etcdPod() Pod {
	return Pod{
		Name: "etcd", NodeName: "minikube", Namespace: "kube-system", CPU: 150, Memory: 150_000,
		Containers: []Container{{Name: "etcd", CPU: 100, Memory: 100_000}, {Name: "side", CPU: 50, Memory: 50_000}},
		Labels:     map[string]string{"app": "web"}, QOS: "Guaranteed",
	}
}

// Round-trips through JSON so assertions check the wire contract the
// frontend reads, not Go struct internals.
func render(t *testing.T, nodes []Node, pods []Pod, axis Axis) []map[string]any {
	t.Helper()
	b, err := json.Marshal(Treemap(nodes, pods, axis))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Groups []map[string]any }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Groups
}

func children(g map[string]any) []map[string]any {
	var out []map[string]any
	for _, c := range g["groups"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func TestTreemapCPU(t *testing.T) {
	n := render(t, []Node{minikube}, []Pod{etcdPod()}, CPU)[0]
	pods := children(n)
	pod, empty := pods[0], pods[1]
	if n["weight"] != 2000.0 || pod["weight"] != 150.0 || empty["weight"] != 1850.0 || empty["color"] != "#ffffff" {
		t.Fatalf("node=%v pod=%v empty=%v", n["weight"], pod["weight"], empty)
	}
	cs := children(pod)
	if cs[0]["weight"] != 100.0 || cs[1]["weight"] != 50.0 {
		t.Fatalf("containers %v", cs)
	}
}

func TestTreemapMemory(t *testing.T) {
	n := render(t, []Node{minikube}, []Pod{etcdPod()}, Memory)[0]
	pods := children(n)
	if n["weight"] != 1_000_000.0 || pods[0]["weight"] != 150_000.0 || pods[1]["weight"] != 850_000.0 {
		t.Fatalf("got %v", pods)
	}
	if cs := children(pods[0]); cs[0]["weight"] != 100_000.0 || cs[1]["weight"] != 50_000.0 {
		t.Fatalf("containers %v", cs)
	}
}

func TestTreemapInitContainersAreGreyAndSkippedWhenZero(t *testing.T) {
	p := Pod{Name: "my-pod", NodeName: "minikube", CPU: 500,
		Containers:     []Container{{Name: "app", CPU: 100}},
		InitContainers: []Container{{Name: "init-db", CPU: 500}, {Name: "init-noop"}}}
	cs := children(children(render(t, []Node{minikube}, []Pod{p}, CPU)[0])[0])
	if len(cs) != 2 || cs[1]["label"] != "init-db (init)" || cs[1]["weight"] != 500.0 || cs[1]["color"] != "#aaaaaa" {
		t.Fatalf("got %v", cs)
	}
	if _, ok := cs[0]["color"]; ok {
		t.Fatal("regular container must carry no color: the frontend reads color as 'init'")
	}
}

func TestTreemapPodMetadata(t *testing.T) {
	withInit := etcdPod()
	withInit.InitContainers = []Container{{Name: "i"}}
	pod := children(render(t, []Node{minikube}, []Pod{withInit}, CPU)[0])[0]
	if pod["namespace"] != "kube-system" || pod["qos"] != "Guaranteed" || pod["hasInitContainers"] != true ||
		fmt.Sprint(pod["labels"]) != "map[app:web]" || fmt.Sprint(pod["findings"]) != "[missing-limits]" {
		t.Fatalf("got %v", pod)
	}
	bare := children(render(t, []Node{minikube}, []Pod{{Name: "x", NodeName: "minikube"}}, CPU)[0])[0]
	if fmt.Sprint(bare["labels"]) != "map[]" || bare["qos"] != "" || fmt.Sprint(bare["findings"]) != "[]" {
		t.Fatalf("neutral values: %v", bare)
	}
}

func TestTreemapEmptyLeafShape(t *testing.T) {
	pods := children(render(t, []Node{minikube}, []Pod{etcdPod()}, CPU)[0])
	if fmt.Sprint(pods[1]) != "map[color:#ffffff label:empty weight:1850]" {
		t.Fatalf("got %v", pods[1])
	}
}

func TestTreemapNodeHealth(t *testing.T) {
	sick := minikube
	sick.Unschedulable = true
	sick.Taints = []Taint{{Key: "gpu", Effect: "NoSchedule"}}
	sick.Conditions = healthy(map[string]bool{"MemoryPressure": true})
	n := render(t, []Node{sick}, nil, CPU)[0]
	if n["unschedulable"] != true || fmt.Sprint(n["warnings"]) != "[cordoned memory-pressure tainted]" ||
		fmt.Sprint(n["taints"]) != "[map[effect:NoSchedule key:gpu value:]]" {
		t.Fatalf("got %v", n)
	}
	bare := render(t, []Node{{Name: "bare"}}, nil, CPU)[0]
	if fmt.Sprint(bare["taints"], bare["conditions"], bare["warnings"]) != "[] map[] []" {
		t.Fatalf("neutral values: %v", bare)
	}
}

func TestTreemapIsSortedAndDropsUnscheduledPods(t *testing.T) {
	nodes := []Node{{Name: "b"}, {Name: "a"}}
	pods := []Pod{
		{Name: "z", Namespace: "ns1", NodeName: "a"},
		{Name: "y", Namespace: "ns2", NodeName: "a"},
		{Name: "x", Namespace: "ns2", NodeName: "a"},
		{Name: "pending", Namespace: "ns1"},
		{Name: "orphan", Namespace: "ns1", NodeName: "gone"},
	}
	groups := render(t, nodes, pods, CPU)
	if groups[0]["label"] != "a" || groups[1]["label"] != "b" {
		t.Fatalf("nodes not sorted: %v", groups)
	}
	var labels []any
	for _, p := range children(groups[0]) {
		labels = append(labels, p["label"])
	}
	if fmt.Sprint(labels) != "[z x y empty]" {
		t.Fatalf("pods not sorted by namespace/name: %v", labels)
	}
}

func BenchmarkTreemap(b *testing.B) {
	var nodes []Node
	var pods []Pod
	for i := range 200 {
		nodes = append(nodes, Node{Name: fmt.Sprintf("node-%03d", i), CPU: 16000, Memory: 64_000_000})
		for j := range 50 {
			pods = append(pods, Pod{Name: fmt.Sprintf("pod-%d-%d", i, j), Namespace: "default", NodeName: nodes[i].Name,
				CPU: 100, Memory: 256_000, Containers: []Container{{Name: "app", CPU: 100, Memory: 256_000}}})
		}
	}
	for b.Loop() {
		if _, err := json.Marshal(Treemap(nodes, pods, CPU)); err != nil {
			b.Fatal(err)
		}
	}
}
