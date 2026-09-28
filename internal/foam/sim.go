package foam

import (
	"cmp"
	"errors"
	"fmt"
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

func gi(kb float64) string { return fmt.Sprintf("%.1fGi", kb*1000/(1<<30)) }

// rejects lists why n refuses p, in the scheduler's filter order; empty means
// it fits. Each reason is "kind" or "kind: detail".
func rejects(p Pod, n Node, r room) []string {
	out := []string{}
	cordon := corev1.Taint{Key: cordonTaint, Effect: corev1.TaintEffectNoSchedule}
	if n.Unschedulable && !schedhelper.TolerationsTolerateTaint(logr.Discard(), p.Tolerations, &cordon, true) {
		out = append(out, "cordoned")
	}
	taints := make([]corev1.Taint, 0, len(n.Taints))
	for _, t := range n.Taints {
		taints = append(taints, corev1.Taint{Key: t.Key, Value: t.Value, Effect: corev1.TaintEffect(t.Effect)})
	}
	if t, ok := schedhelper.FindMatchingUntoleratedTaint(logr.Discard(), taints, p.Tolerations, noScheduleOrExecute, true); ok {
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
		out = append(out, fmt.Sprintf("insufficient memory: requires %s, available %s", gi(p.Memory), gi(max(r.memory, 0))))
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
