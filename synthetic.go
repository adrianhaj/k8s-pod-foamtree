package main

import (
	"context"
	"fmt"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree/internal/kube"
)

// syntheticSource is a made-up cluster for UI work, demos and scale tests;
// it never talks to an API server. Every syntheticChurn it replaces 1 pod in
// 50, so refresh animations have something to show. Churn follows the clock,
// not the call count: one refresh asks twice (CPU, then memory) and both
// answers must list the same pods.
type syntheticSource struct {
	nodes, podsPerNode int
	now                func() time.Time
}

const syntheticChurn = 5 * time.Second

func parseSynthetic(spec string) (*syntheticSource, error) {
	var n, m int
	if _, err := fmt.Sscanf(spec, "%dx%d", &n, &m); err != nil || n < 1 || m < 1 || n > 200_000 || m > 200_000 || n*m > 200_000 {
		return nil, fmt.Errorf("--synthetic wants NODESxPODS_PER_NODE, at most 200000 pods, got %q", spec)
	}
	return &syntheticSource{nodes: n, podsPerNode: m, now: time.Now}, nil
}

func (s *syntheticSource) Contexts() ([]kube.Context, error) {
	return []kube.Context{{Context: "synthetic", Active: true}}, nil
}

func (s *syntheticSource) Snapshot(context.Context, string) ([]foam.Node, []foam.Pod, error) {
	gen := int(s.now().UnixNano() / int64(syntheticChurn))
	pools := []string{"general", "general", "memory", "spot"}
	nodes := make([]foam.Node, 0, s.nodes)
	pods := make([]foam.Pod, 0, s.nodes*s.podsPerNode)
	for i := range s.nodes {
		n := foam.Node{
			Name: fmt.Sprintf("node-%04d", i), CPU: 16_000, Memory: 64_000_000,
			Conditions: map[string]bool{"Ready": true, "MemoryPressure": i%31 == 5},
			Zone:       "synthetic-1" + string(rune('a'+i%3)), Region: "synthetic-1",
			InstanceType: "m.4xlarge", Pool: pools[i%len(pools)], Unschedulable: i%23 == 7,
		}
		switch n.Pool {
		case "memory":
			n.Memory, n.InstanceType = 128_000_000, "r.4xlarge"
		case "spot":
			n.Taints = []foam.Taint{{Key: "spot", Value: "true", Effect: "NoSchedule"}}
		}
		nodes = append(nodes, n)
		for j := range s.podsPerNode {
			k := i*s.podsPerNode + j
			cpu, mem := int64(25+(k*37)%400), float64(50_000+(k*7919)%1_500_000)
			qos := "Burstable"
			switch {
			case k%13 == 0:
				cpu, mem, qos = 0, 0, "BestEffort"
			case k%5 == 0:
				qos = "Guaranteed"
			}
			c := foam.Container{Name: "app", CPU: cpu, Memory: mem}
			p := foam.Pod{
				// The last segment changes for pod k once every 50 generations.
				Name:      fmt.Sprintf("svc%02d-%x-%x", k%40, k, (k+gen)/50),
				Namespace: fmt.Sprintf("team-%d", k%9), NodeName: n.Name, CPU: cpu, Memory: mem,
				Labels: map[string]string{"app": fmt.Sprintf("svc%02d", k%40)}, QOS: qos,
			}
			switch qos {
			case "Guaranteed":
				cpuLim, memLim := cpu, mem
				c.MemoryLimit, p.MemoryLimit, p.CPULimit = &memLim, &memLim, &cpuLim
			case "Burstable":
				if k%11 != 0 {
					lim := mem * 1.5
					c.MemoryLimit, p.MemoryLimit = &lim, &lim
				}
				if k%3 == 0 {
					lim := cpu * 2
					p.CPULimit = &lim
				}
				// Only here: an init container without limits makes any pod Burstable.
				if k%17 == 0 {
					p.InitContainers = []foam.Container{{Name: "init", CPU: 100, Memory: 10_000}}
				}
			}
			p.Containers = []foam.Container{c}
			pods = append(pods, p)
		}
	}
	return nodes, pods, nil
}
