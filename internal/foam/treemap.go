package foam

import (
	"cmp"
	"slices"
)

type Axis int

const (
	CPU Axis = iota
	Memory
)

func (a Axis) container(c Container) float64 {
	if a == CPU {
		return float64(c.CPU)
	}
	return c.Memory
}

func (a Axis) pod(p Pod) float64 {
	if a == CPU {
		return float64(p.CPU)
	}
	return p.Memory
}

// limit is nil for a pod with no ceiling on this axis.
func (a Axis) limit(p Pod) *float64 {
	if a == CPU {
		if p.CPULimit == nil {
			return nil
		}
		v := float64(*p.CPULimit)
		return &v
	}
	return p.MemoryLimit
}

func (a Axis) node(n Node) float64 {
	if a == CPU {
		return float64(n.CPU)
	}
	return n.Memory
}

type Tree struct {
	Groups []NodeGroup `json:"groups"`
}

type NodeGroup struct {
	Label         string          `json:"label"`
	Weight        float64         `json:"weight"`
	Groups        []any           `json:"groups"` // PodGroup..., then the "empty" Leaf
	Unschedulable bool            `json:"unschedulable"`
	Taints        []Taint         `json:"taints"`
	Conditions    map[string]bool `json:"conditions"`
	Warnings      []string        `json:"warnings"`
	Zone          string          `json:"zone"`
	Region        string          `json:"region"`
	InstanceType  string          `json:"instanceType"`
	Pool          string          `json:"pool"`
	CapacityType  string          `json:"capacityType"`
}

type PodGroup struct {
	Label             string            `json:"label"`
	Weight            float64           `json:"weight"`
	Groups            []Leaf            `json:"groups"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels"`
	QOS               string            `json:"qos"`
	HasInitContainers bool              `json:"hasInitContainers"`
	Findings          []string          `json:"findings"`
	Limit             *float64          `json:"limit"`
}

type Leaf struct {
	Label  string  `json:"label"`
	Weight float64 `json:"weight"`
	Color  string  `json:"color,omitempty"`
}

const (
	emptyColor = "#ffffff"
	// The frontend tells init containers apart by the presence of a color.
	initColor = "#aaaaaa"
)

// Treemap nests node → pod → container on one axis and adds an "empty" leaf
// per node for free capacity. Output is sorted by name: the informer cache
// has no stable order, and a reshuffled layout on every refresh is unusable.
func Treemap(nodes []Node, pods []Pod, axis Axis) Tree {
	byNode := map[string][]Pod{}
	for _, p := range pods {
		byNode[p.NodeName] = append(byNode[p.NodeName], p)
	}
	nodes = slices.SortedFunc(slices.Values(nodes), func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })

	tree := Tree{Groups: make([]NodeGroup, 0, len(nodes))}
	for _, n := range nodes {
		onNode := byNode[n.Name]
		slices.SortFunc(onNode, func(a, b Pod) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})
		groups := make([]any, 0, len(onNode)+1)
		used := 0.0
		for _, p := range onNode {
			groups = append(groups, podGroup(p, n, axis))
			used += axis.pod(p)
		}
		groups = append(groups, Leaf{Label: "empty", Weight: axis.node(n) - used, Color: emptyColor})
		tree.Groups = append(tree.Groups, NodeGroup{
			Label:         n.Name,
			Weight:        axis.node(n),
			Groups:        groups,
			Unschedulable: n.Unschedulable,
			Taints:        orEmpty(n.Taints),
			Conditions:    orEmptyMap(n.Conditions),
			Warnings:      Warnings(n),
			Zone:          n.Zone,
			Region:        n.Region,
			InstanceType:  n.InstanceType,
			Pool:          n.Pool,
			CapacityType:  n.CapacityType,
		})
	}
	return tree
}

func podGroup(p Pod, n Node, axis Axis) PodGroup {
	leaves := make([]Leaf, 0, len(p.Containers)+len(p.InitContainers))
	for _, c := range p.Containers {
		leaves = append(leaves, Leaf{Label: c.Name, Weight: axis.container(c)})
	}
	for _, c := range p.InitContainers {
		if w := axis.container(c); w > 0 {
			leaves = append(leaves, Leaf{Label: c.Name + " (init)", Weight: w, Color: initColor})
		}
	}
	return PodGroup{
		Label:             p.Name,
		Weight:            axis.pod(p),
		Groups:            leaves,
		Namespace:         p.Namespace,
		Labels:            orEmptyMap(p.Labels),
		QOS:               p.QOS,
		HasInitContainers: len(p.InitContainers) > 0,
		Findings:          Findings(p, n),
		Limit:             axis.limit(p),
	}
}

// JSON null would break the frontend's `.length` / key lookups.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func orEmptyMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}
