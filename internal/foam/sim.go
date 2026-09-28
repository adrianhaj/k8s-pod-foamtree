package foam

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	schedhelper "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
)

// A dry run of the kube-scheduler's filter phase, on the cached snapshot.
// Modelled: NodeUnschedulable, TaintToleration, NodeAffinity (nodeSelector
// and required node affinity) and NodeResourcesFit (cpu, memory, pod count).
// ponytail: no inter-pod affinity, topology spread, host ports, volume
// zones, extended resources or preemption — each needs data the cache does
// not hold (PVs, other pods' labels per topology) or RBAC it does not have.

// room is what a node still hands out: allocatable minus bound requests.
type room struct {
	cpu    int64
	memory float64
	pods   int64
}

func free(nodes []Node, pods []Pod) map[string]room {
	out := make(map[string]room, len(nodes))
	for _, n := range nodes {
		out[n.Name] = room{n.AllocCPU, n.AllocMemory, n.AllocPods}
	}
	for _, p := range pods {
		if r, ok := out[p.NodeName]; ok {
			out[p.NodeName] = room{r.cpu - p.CPU, r.memory - p.Memory, r.pods - 1}
		}
	}
	return out
}

func noScheduleOrExecute(t *corev1.Taint) bool {
	return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
}

// humanMem formats a decimal-kB amount as Gi, falling back to Mi below 1Gi
// so a small amount doesn't print as "0.0Gi".
func humanMem(kb float64) string {
	if gib := kb * 1000 / (1 << 30); gib >= 1 {
		return fmt.Sprintf("%.1fGi", gib)
	}
	return fmt.Sprintf("%.1fMi", kb*1000/(1<<20))
}

// rejects lists why n refuses p, in the scheduler's filter order; empty means
// it fits. Each reason is "kind" or "kind: detail".
func rejects(p Pod, n Node, r room) []string {
	out := []string{}
	cordon := corev1.Taint{Key: cordonTaint, Effect: corev1.TaintEffectNoSchedule}
	if n.Unschedulable && !schedhelper.TolerationsTolerateTaint(logr.Discard(), p.Tolerations, &cordon, false) {
		out = append(out, "cordoned")
	}
	taints := make([]corev1.Taint, 0, len(n.Taints))
	for _, t := range n.Taints {
		taints = append(taints, corev1.Taint{Key: t.Key, Value: t.Value, Effect: corev1.TaintEffect(t.Effect)})
	}
	if t, ok := schedhelper.FindMatchingUntoleratedTaint(logr.Discard(), taints, p.Tolerations, noScheduleOrExecute, false); ok {
		out = append(out, "untolerated taint: "+t.ToString())
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n.Name, Labels: n.Labels}}
	if ok, err := nodeaffinity.NewRequiredNodeAffinity(p.NodeSelector, p.Affinity).Match(node); !ok || err != nil {
		out = append(out, "node selector mismatch")
	}
	if p.CPU > 0 && p.CPU > r.cpu {
		out = append(out, fmt.Sprintf("insufficient cpu: requires %dm, available %dm", p.CPU, max(r.cpu, 0)))
	}
	if p.Memory > 0 && p.Memory > r.memory {
		out = append(out, fmt.Sprintf("insufficient memory: requires %s, available %s", humanMem(p.Memory), humanMem(max(r.memory, 0))))
	}
	if r.pods < 1 {
		out = append(out, fmt.Sprintf("too many pods: %d allocatable", n.AllocPods))
	}
	return out
}

type Verdict struct {
	Node    string   `json:"node"`
	Reasons []string `json:"reasons"` // empty: the pod fits
}

// Fit reports, per node sorted by name, why p would not schedule there.
func Fit(nodes []Node, pods []Pod, p Pod) []Verdict {
	rooms := free(nodes, pods)
	nodes = slices.SortedFunc(slices.Values(nodes), func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })
	out := make([]Verdict, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, Verdict{Node: n.Name, Reasons: rejects(p, n, rooms[n.Name])})
	}
	return out
}

var effects = []corev1.TaintEffect{"", corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute}

// Hypothetical builds the pod a user asks about. Tolerations use the
// kubectl taint syntax, comma-separated: key[=value][:effect].
func Hypothetical(cpu, memory, nodeSelector, tolerations string) (Pod, error) {
	var p Pod
	quantity := func(name, s string) (resource.Quantity, error) {
		if s == "" {
			return resource.Quantity{}, nil
		}
		q, err := resource.ParseQuantity(s)
		if err == nil && q.Sign() < 0 {
			err = errors.New("negative")
		}
		if err == nil && q.CmpInt64(math.MaxInt64/1000) > 0 {
			err = errors.New("too large")
		}
		if err != nil {
			return q, fmt.Errorf("%s %q: %w", name, s, err)
		}
		return q, nil
	}
	c, err := quantity("cpu", cpu)
	if err != nil {
		return p, err
	}
	m, err := quantity("memory", memory)
	if err != nil {
		return p, err
	}
	p.CPU, p.Memory = c.MilliValue(), kB(m)
	if p.NodeSelector, err = labels.ConvertSelectorToLabelsMap(nodeSelector); err != nil {
		return p, fmt.Errorf("node selector %q: %w", nodeSelector, err)
	}
	for _, s := range strings.Split(tolerations, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		kv, effect, _ := strings.Cut(s, ":")
		key, value, hasValue := strings.Cut(kv, "=")
		t := corev1.Toleration{Key: key, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffect(effect)}
		if hasValue {
			t.Operator, t.Value = corev1.TolerationOpEqual, value
		}
		if key == "" || !slices.Contains(effects, t.Effect) {
			return p, fmt.Errorf("toleration %q: want key[=value][:NoSchedule|PreferNoSchedule|NoExecute]", s)
		}
		p.Tolerations = append(p.Tolerations, t)
	}
	return p, nil
}

type Placement struct {
	Pod    string `json:"pod"` // namespace/name
	Node   string `json:"node,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type DrainResult struct {
	Moved   []Placement `json:"moved"`
	Pending []Placement `json:"pending"`
	// DaemonSet and static pods go down with the node; nothing reschedules them.
	Ignored []string `json:"ignored"`
	// No controller: deleted and never recreated (kubectl drain wants --force).
	Unmanaged []string `json:"unmanaged"`
}

// Drain replays evicting every pod on the named node onto the others, as
// `kubectl drain` or a node failure would; false when there is no such node.
// ponytail: greedy, largest request first, each onto the feasible node with
// the most free share (the scheduler's default LeastAllocated score). No
// PodDisruptionBudgets (needs RBAC on policy), no optimal packing: a real
// rollout can fragment differently.
func Drain(nodes []Node, pods []Pod, name string) (DrainResult, bool) {
	if !slices.ContainsFunc(nodes, func(n Node) bool { return n.Name == name }) {
		return DrainResult{}, false
	}
	rooms := free(nodes, pods)
	targets := slices.DeleteFunc(slices.Clone(nodes), func(n Node) bool { return n.Name == name })
	slices.SortFunc(targets, func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })

	out := DrainResult{Moved: []Placement{}, Pending: []Placement{}, Ignored: []string{}, Unmanaged: []string{}}
	var evicted []Pod
	for _, p := range pods {
		if p.NodeName != name {
			continue
		}
		switch p.Controller {
		case "DaemonSet", "Node":
			out.Ignored = append(out.Ignored, p.Namespace+"/"+p.Name)
		case "":
			out.Unmanaged = append(out.Unmanaged, p.Namespace+"/"+p.Name)
		default:
			evicted = append(evicted, p)
		}
	}
	slices.Sort(out.Ignored)
	slices.Sort(out.Unmanaged)
	slices.SortFunc(evicted, func(a, b Pod) int {
		return cmp.Or(cmp.Compare(b.CPU, a.CPU), cmp.Compare(b.Memory, a.Memory),
			cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})

	for _, p := range evicted {
		best, bestScore := -1, 0.0
		why := map[string]int{}
		for i, n := range targets {
			r := rooms[n.Name]
			if rs := rejects(p, n, r); len(rs) > 0 {
				for _, reason := range rs {
					kind, _, _ := strings.Cut(reason, ":")
					why[kind]++
				}
				continue
			}
			score := (float64(r.cpu-p.CPU)/float64(n.AllocCPU) + (r.memory-p.Memory)/n.AllocMemory) / 2
			if best < 0 || score > bestScore {
				best, bestScore = i, score
			}
		}
		id := p.Namespace + "/" + p.Name
		if best < 0 {
			out.Pending = append(out.Pending, Placement{Pod: id, Reason: summary(why, len(targets))})
			continue
		}
		n := targets[best].Name
		r := rooms[n]
		rooms[n] = room{r.cpu - p.CPU, r.memory - p.Memory, r.pods - 1}
		out.Moved = append(out.Moved, Placement{Pod: id, Node: n})
	}
	return out, true
}

// summary reads like the scheduler's FailedScheduling event, most common first.
func summary(why map[string]int, nodes int) string {
	kinds := slices.Collect(maps.Keys(why))
	slices.SortFunc(kinds, func(a, b string) int { return cmp.Or(cmp.Compare(why[b], why[a]), cmp.Compare(a, b)) })
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", why[k], k))
	}
	out := fmt.Sprintf("0/%d nodes are available", nodes)
	if len(parts) > 0 {
		out += ": " + strings.Join(parts, ", ")
	}
	return out
}
