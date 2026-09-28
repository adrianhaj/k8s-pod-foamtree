package main

import (
	"context"
	"testing"
	"time"
)

func TestParseSynthetic(t *testing.T) {
	for _, bad := range []string{"", "x", "abc", "0x5", "5x0", "-1x5", "1000x1000", "4294967296x4294967296"} {
		if _, err := parseSynthetic(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s, err := parseSynthetic("100x50"); err != nil || s.nodes != 100 || s.podsPerNode != 50 {
		t.Fatalf("got %+v %v", s, err)
	}
}

func TestSyntheticSnapshot(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }
	nodes, pods, err := s.Snapshot(context.Background(), "")
	if err != nil || len(nodes) != 30 || len(pods) != 600 {
		t.Fatalf("nodes=%d pods=%d err=%v", len(nodes), len(pods), err)
	}
	zones, pools := map[string]bool{}, map[string]bool{}
	for _, n := range nodes {
		zones[n.Zone], pools[n.Pool] = true, true
	}
	unboundedMem, boundedCPU := 0, 0
	for _, p := range pods {
		if p.MemoryLimit == nil {
			unboundedMem++
		}
		if p.CPULimit != nil {
			boundedCPU++
		}
	}
	if len(zones) != 3 || len(pools) < 3 || unboundedMem == 0 || boundedCPU == 0 || boundedCPU == len(pods) {
		t.Fatalf("not varied enough: zones=%v pools=%v unboundedMem=%d boundedCPU=%d", zones, pools, unboundedMem, boundedCPU)
	}

	// The CPU and memory requests of one refresh must see the same pods.
	_, same, _ := s.Snapshot(context.Background(), "")
	for i := range pods {
		if pods[i].Name != same[i].Name {
			t.Fatalf("pods changed within one churn window: %s vs %s", pods[i].Name, same[i].Name)
		}
	}

	clock = clock.Add(syntheticChurn)
	_, next, _ := s.Snapshot(context.Background(), "")
	names := map[string]bool{}
	for _, p := range next {
		names[p.Name] = true
	}
	replaced := 0
	for _, p := range pods {
		if !names[p.Name] {
			replaced++
		}
	}
	if replaced != len(pods)/50 {
		t.Fatalf("replaced %d pods between refreshes, want %d", replaced, len(pods)/50)
	}
}

func TestSyntheticFlag(t *testing.T) {
	o, err := parseFlags([]string{"--synthetic", "10x5"})
	if err != nil || o.synthetic == nil || o.synthetic.nodes != 10 {
		t.Fatalf("got %+v %v", o.synthetic, err)
	}
	if _, err := parseFlags([]string{"--synthetic", "lots"}); err == nil {
		t.Fatal("bad --synthetic accepted")
	}
}

// Every QoS class shows up, and each pod's requests and limits agree with its
// class, so Color by → QoS and the eviction panel have real data to show.
func TestSyntheticQoS(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	_, pods, _ := s.Snapshot(context.Background(), "")
	count := map[string]int{}
	for _, p := range pods {
		count[p.QOS]++
		switch p.QOS {
		case "BestEffort":
			if p.CPU != 0 || p.Memory != 0 || p.CPULimit != nil || p.MemoryLimit != nil || len(p.InitContainers) > 0 {
				t.Fatalf("BestEffort pod with requests or limits: %+v", p)
			}
		case "Guaranteed":
			if p.CPULimit == nil || *p.CPULimit != p.CPU || p.MemoryLimit == nil || *p.MemoryLimit != p.Memory || len(p.InitContainers) > 0 {
				t.Fatalf("Guaranteed pod with limits != requests: %+v", p)
			}
		}
	}
	if len(count) != 3 || count["BestEffort"] == 0 || count["Guaranteed"] == 0 || count["Burstable"] == 0 {
		t.Fatalf("QoS classes: %v", count)
	}
}
