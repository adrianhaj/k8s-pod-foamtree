// Package foam turns Kubernetes nodes and pods into the treemap JSON the
// dashboard renders. It is pure: no client-go calls, only API types in.
package foam

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	resourcehelper "k8s.io/component-helpers/resource"
)

// CPU is in millicores, memory in decimal kB — the units the frontend expects.
type Container struct {
	Name   string
	CPU    int64
	Memory float64
	// nil when unset: an unbounded container is exactly what the audit flags.
	MemoryLimit *float64
}

type Pod struct {
	Name, NodeName, Namespace string
	// Effective request — what the scheduler reserves for the pod.
	CPU            int64
	Memory         float64
	Containers     []Container
	InitContainers []Container
	Labels         map[string]string
	QOS            string
	// nil when any running container is unbounded on that axis.
	CPULimit    *int64
	MemoryLimit *float64
}

type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type Node struct {
	Name          string
	CPU           int64
	Memory        float64
	Unschedulable bool
	Taints        []Taint
	Conditions    map[string]bool
	// "" when the node does not carry the label.
	Zone, Region, InstanceType, Pool string
}

// Kubernetes adds this taint itself on cordon; spec.unschedulable already
// reports it, so counting it would mark every cordoned node twice.
const cordonTaint = "node.kubernetes.io/unschedulable"

var pressureConditions = []string{"MemoryPressure", "DiskPressure", "PIDPressure"}

const (
	zoneLabel         = "topology.kubernetes.io/zone"
	regionLabel       = "topology.kubernetes.io/region"
	instanceTypeLabel = "node.kubernetes.io/instance-type"
)

// Node pool labels by provider, most specific first: Karpenter also runs on
// EKS nodes that carry a nodegroup label.
var PoolLabels = []string{
	"karpenter.sh/nodepool",
	"eks.amazonaws.com/nodegroup",
	"cloud.google.com/gke-nodepool",
	"kubernetes.azure.com/agentpool",
}

// TopologyLabels are the node labels the dashboard reads.
var TopologyLabels = append([]string{zoneLabel, regionLabel, instanceTypeLabel}, PoolLabels...)

func kB(q resource.Quantity) float64 { return float64(q.Value()) / 1000 }

func container(c corev1.Container) Container {
	out := Container{
		Name:   c.Name,
		CPU:    c.Resources.Requests.Cpu().MilliValue(),
		Memory: kB(*c.Resources.Requests.Memory()),
	}
	if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
		v := kB(q)
		out.MemoryLimit = &v
	}
	return out
}

// bounded reports whether a pod has a ceiling on r: the pod sets one itself,
// or every container that keeps running (regular ones and sidecars) does.
// Plain init containers finish first, so they never bound the running pod.
func bounded(p *corev1.Pod, r corev1.ResourceName) bool {
	if p.Spec.Resources != nil {
		if _, ok := p.Spec.Resources.Limits[r]; ok {
			return true
		}
	}
	for _, c := range p.Spec.Containers {
		if _, ok := c.Resources.Limits[r]; !ok {
			return false
		}
	}
	for _, c := range p.Spec.InitContainers {
		sidecar := c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
		if _, ok := c.Resources.Limits[r]; sidecar && !ok {
			return false
		}
	}
	return len(p.Spec.Containers) > 0
}

func containers(cs []corev1.Container) []Container {
	out := make([]Container, 0, len(cs))
	for _, c := range cs {
		out = append(out, container(c))
	}
	return out
}

func FromPod(p *corev1.Pod) Pod {
	// The scheduler's own formula: max(sum regular, max init), sidecars,
	// pod overhead and pod-level resources included.
	req := resourcehelper.PodRequests(p, resourcehelper.PodResourcesOptions{})
	labels := p.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	out := Pod{
		Name:           p.Name,
		NodeName:       p.Spec.NodeName,
		Namespace:      p.Namespace,
		CPU:            req.Cpu().MilliValue(),
		Memory:         kB(*req.Memory()),
		Containers:     containers(p.Spec.Containers),
		InitContainers: containers(p.Spec.InitContainers),
		Labels:         labels,
		QOS:            string(p.Status.QOSClass),
	}
	lim := resourcehelper.PodLimits(p, resourcehelper.PodResourcesOptions{})
	if bounded(p, corev1.ResourceCPU) {
		v := lim.Cpu().MilliValue()
		out.CPULimit = &v
	}
	if bounded(p, corev1.ResourceMemory) {
		v := kB(*lim.Memory())
		out.MemoryLimit = &v
	}
	return out
}

func FromNode(n *corev1.Node) Node {
	taints := []Taint{}
	for _, t := range n.Spec.Taints {
		if t.Key != cordonTaint {
			taints = append(taints, Taint{Key: t.Key, Value: t.Value, Effect: string(t.Effect)})
		}
	}
	status := map[corev1.NodeConditionType]corev1.ConditionStatus{}
	for _, c := range n.Status.Conditions {
		status[c.Type] = c.Status
	}
	conditions := map[string]bool{}
	for _, name := range pressureConditions {
		conditions[name] = status[corev1.NodeConditionType(name)] == corev1.ConditionTrue
	}
	// Unknown means the API server lost the kubelet: not ready. No Ready
	// condition at all means nothing was reported, so don't invent an outage.
	ready, ok := status[corev1.NodeReady]
	conditions["Ready"] = !ok || ready == corev1.ConditionTrue
	pool := ""
	for _, l := range PoolLabels {
		if v := n.Labels[l]; v != "" {
			pool = v
			break
		}
	}
	return Node{
		Name:          n.Name,
		CPU:           n.Status.Capacity.Cpu().MilliValue(),
		Memory:        kB(*n.Status.Capacity.Memory()),
		Unschedulable: n.Spec.Unschedulable,
		Taints:        taints,
		Conditions:    conditions,
		Zone:          n.Labels[zoneLabel],
		Region:        n.Labels[regionLabel],
		InstanceType:  n.Labels[instanceTypeLabel],
		Pool:          pool,
	}
}
