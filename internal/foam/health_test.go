package foam

import (
	"reflect"
	"testing"
)

func healthy(overrides map[string]bool) map[string]bool {
	c := map[string]bool{"MemoryPressure": false, "DiskPressure": false, "PIDPressure": false, "Ready": true}
	for k, v := range overrides {
		c[k] = v
	}
	return c
}

func TestWarnings(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want []string
	}{
		{"healthy", Node{Conditions: healthy(nil)}, []string{}},
		{"no metadata at all", Node{}, []string{}},
		{"cordoned", Node{Unschedulable: true, Conditions: healthy(nil)}, []string{"cordoned"}},
		{"memory pressure", Node{Conditions: healthy(map[string]bool{"MemoryPressure": true})}, []string{"memory-pressure"}},
		{"disk pressure", Node{Conditions: healthy(map[string]bool{"DiskPressure": true})}, []string{"disk-pressure"}},
		{"pid pressure", Node{Conditions: healthy(map[string]bool{"PIDPressure": true})}, []string{"pid-pressure"}},
		{"not ready", Node{Conditions: healthy(map[string]bool{"Ready": false})}, []string{"not-ready"}},
		{"missing Ready key", Node{Conditions: map[string]bool{"MemoryPressure": false}}, []string{}},
		{"NoSchedule", Node{Taints: []Taint{{Key: "gpu-only", Effect: "NoSchedule"}}}, []string{"tainted"}},
		{"NoExecute", Node{Taints: []Taint{{Key: "evict-me", Effect: "NoExecute"}}}, []string{"tainted"}},
		{"PreferNoSchedule alone", Node{Taints: []Taint{{Key: "spot", Effect: "PreferNoSchedule"}}}, []string{}},
		{"PreferNoSchedule + blocking", Node{Taints: []Taint{{Key: "spot", Effect: "PreferNoSchedule"}, {Key: "gpu", Effect: "NoSchedule"}}}, []string{"tainted"}},
		{"worst first", Node{
			Unschedulable: true,
			Taints:        []Taint{{Key: "gpu", Effect: "NoSchedule"}},
			Conditions:    healthy(map[string]bool{"Ready": false, "MemoryPressure": true, "DiskPressure": true}),
		}, []string{"cordoned", "not-ready", "memory-pressure", "disk-pressure", "tainted"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Warnings(tc.node); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// 4 cores / 16000 MB — a 1:4 core:GB node.
var worker = Node{Name: "worker", CPU: 4000, Memory: 16_000_000}

func auditPod(cpu int64, mem float64, limit bool, inits ...Container) Pod {
	c := Container{Name: "app", CPU: cpu, Memory: mem}
	if limit {
		one := 1.0
		c.MemoryLimit = &one
	}
	return Pod{Name: "app", NodeName: "worker", CPU: cpu, Memory: mem, Containers: []Container{c}, InitContainers: inits}
}

func TestFindings(t *testing.T) {
	cases := []struct {
		name string
		pod  Pod
		node Node
		want []string
	}{
		{"well sized", auditPod(400, 1_600_000, true), worker, []string{}},
		{"no cpu request", auditPod(0, 1_600_000, true), worker, []string{"missing-requests"}},
		{"no memory request", auditPod(400, 0, true), worker, []string{"missing-requests"}},
		{"no memory limit", auditPod(400, 1_600_000, false), worker, []string{"missing-limits"}},
		{"init containers not audited", auditPod(400, 1_600_000, true, Container{Name: "init"}), worker, []string{}},
		{"exactly 80% is fine", auditPod(3200, 12_800_000, true), worker, []string{}},
		{"lopsided", auditPod(2000, 800_000, true), worker, []string{"ratio-asymmetry"}},
		{"small lopsided", auditPod(200, 16_000, true), worker, []string{}},
		{"node without capacity", auditPod(400, 1_600_000, true), Node{Name: "ghost"}, []string{}},
		{"all in order", auditPod(3600, 1_600_000, false), worker, []string{"missing-limits", "monolith", "ratio-asymmetry"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Findings(tc.pod, tc.node); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	for _, p := range []Pod{auditPod(3600, 14_400_000, true), auditPod(3600, 13_000_000, true)} {
		if f := Findings(p, worker); !reflect.DeepEqual(f[:1], []string{"monolith"}) {
			t.Errorf("monolith expected in %v", f)
		}
	}
}
