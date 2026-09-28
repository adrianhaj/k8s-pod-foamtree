package foam

import (
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ctr(name, cpu, mem string) corev1.Container {
	req := corev1.ResourceList{}
	if cpu != "" {
		req[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		req[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return corev1.Container{Name: name, Resources: corev1.ResourceRequirements{Requests: req}}
}

func withMemLimit(c corev1.Container, limit string) corev1.Container {
	c.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)}
	return c
}

func pod(containers []corev1.Container, inits ...corev1.Container) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: "kube-system"},
		Spec:       corev1.PodSpec{NodeName: "master", Containers: containers, InitContainers: inits},
	}
}

func TestFromPodSingleContainer(t *testing.T) {
	p := FromPod(pod([]corev1.Container{ctr("etcd", "100m", "1G")}))
	if p.Name != "etcd" || p.NodeName != "master" || p.CPU != 100 || p.Memory != 1_000_000 {
		t.Fatalf("got %+v", p)
	}
	if len(p.InitContainers) != 0 || p.Containers[0].CPU != 100 || p.Containers[0].Memory != 1_000_000 {
		t.Fatalf("containers %+v", p.Containers)
	}
}

func TestFromPodSumsRegularContainers(t *testing.T) {
	p := FromPod(pod([]corev1.Container{ctr("etcd", "100m", "1G"), ctr("side", "50m", "100Mi")}))
	if p.CPU != 150 || p.Memory != 1_000_000+104_857.6 {
		t.Fatalf("got cpu=%d mem=%v", p.CPU, p.Memory)
	}
}

func TestFromPodWithoutRequestsIsZero(t *testing.T) {
	p := FromPod(pod([]corev1.Container{ctr("etcd", "", "")}))
	if p.CPU != 0 || p.Memory != 0 || p.Containers[0].CPU != 0 || p.Containers[0].Memory != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestFromPodEffectiveRequest(t *testing.T) {
	regular := []corev1.Container{ctr("a", "100m", "100Mi"), ctr("b", "200m", "100Mi")}
	cases := []struct {
		name    string
		inits   []corev1.Container
		wantCPU int64
		wantMem float64
	}{
		{"no init", nil, 300, 209_715.2},
		{"init below sum", []corev1.Container{ctr("init-a", "200m", "")}, 300, 209_715.2},
		{"init above sum", []corev1.Container{ctr("init-a", "500m", "")}, 500, 209_715.2},
		{"max of inits", []corev1.Container{ctr("i1", "500m", ""), ctr("i2", "400m", "")}, 500, 209_715.2},
		{"init without requests", []corev1.Container{ctr("i", "", "")}, 300, 209_715.2},
		{"init dominates memory", []corev1.Container{ctr("i", "", "500Mi")}, 300, 524_288},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := FromPod(pod(regular, tc.inits...))
			if p.CPU != tc.wantCPU || p.Memory != tc.wantMem {
				t.Fatalf("got cpu=%d mem=%v, want %d %v", p.CPU, p.Memory, tc.wantCPU, tc.wantMem)
			}
			if len(p.InitContainers) != len(tc.inits) {
				t.Fatalf("init containers %+v", p.InitContainers)
			}
		})
	}
}

// Native sidecars (restartPolicy: Always) keep running next to the app, so the
// scheduler adds them instead of maxing them. The Python port got this wrong.
func TestFromPodSidecarIsSummed(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	sidecar := ctr("proxy", "50m", "")
	sidecar.RestartPolicy = &always
	p := FromPod(pod([]corev1.Container{ctr("app", "100m", "")}, sidecar))
	if p.CPU != 150 {
		t.Fatalf("cpu=%d, want 150", p.CPU)
	}
}

// The Python parser crashed on 1.5Gi and 100k; 1e3 and plain bytes it handled.
func TestFromPodParsesEveryQuantityFormat(t *testing.T) {
	cases := map[string]float64{"1.5Gi": 1_610_612.736, "100k": 100, "1e3": 1, "128974848": 128_974.848}
	for q, want := range cases {
		if got := FromPod(pod([]corev1.Container{ctr("a", "", q)})).Memory; got != want {
			t.Errorf("%s: got %v kB, want %v", q, got, want)
		}
	}
	if got := FromPod(pod([]corev1.Container{ctr("a", "0.5", "")})).CPU; got != 500 {
		t.Errorf("0.5 cpu: got %d", got)
	}
}

func TestFromPodSelectorMetadata(t *testing.T) {
	p := pod([]corev1.Container{ctr("etcd", "100m", "1G")})
	p.Labels = map[string]string{"app": "etcd", "tier": "control-plane"}
	p.Status.QOSClass = corev1.PodQOSGuaranteed
	got := FromPod(p)
	if got.Namespace != "kube-system" || got.QOS != "Guaranteed" || !reflect.DeepEqual(got.Labels, p.Labels) {
		t.Fatalf("got %+v", got)
	}
	bare := FromPod(pod([]corev1.Container{ctr("etcd", "100m", "1G")}))
	if bare.Labels == nil || len(bare.Labels) != 0 || bare.QOS != "" {
		t.Fatalf("unset labels/qos: %+v", bare)
	}
}

func TestFromPodMemoryLimit(t *testing.T) {
	p := FromPod(pod([]corev1.Container{withMemLimit(ctr("a", "100m", "100Mi"), "256Mi"), ctr("b", "100m", "100Mi")}))
	if l := p.Containers[0].MemoryLimit; l == nil || *l != 268_435.456 {
		t.Fatalf("limit %v", l)
	}
	if p.Containers[1].MemoryLimit != nil {
		t.Fatal("unset limit must be nil")
	}
}

func node(conds ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "minikube"},
		Status: corev1.NodeStatus{
			Capacity:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Conditions: conds,
		},
	}
}

func cond(t corev1.NodeConditionType, s corev1.ConditionStatus) corev1.NodeCondition {
	return corev1.NodeCondition{Type: t, Status: s}
}

func TestFromNodeHealthy(t *testing.T) {
	n := FromNode(node(cond(corev1.NodeReady, corev1.ConditionTrue)))
	want := map[string]bool{"MemoryPressure": false, "DiskPressure": false, "PIDPressure": false, "Ready": true}
	if n.Name != "minikube" || n.CPU != 2000 || n.Memory != 1_073_741.824 || n.Unschedulable ||
		len(n.Taints) != 0 || !reflect.DeepEqual(n.Conditions, want) {
		t.Fatalf("got %+v", n)
	}
}

func TestFromNodeTaintsDropCordonTaint(t *testing.T) {
	raw := node()
	raw.Spec.Unschedulable = true
	raw.Spec.Taints = []corev1.Taint{
		{Key: "node.kubernetes.io/unschedulable", Effect: corev1.TaintEffectNoSchedule},
		{Key: "nvidia.com/gpu", Value: "true", Effect: corev1.TaintEffectNoSchedule},
		{Key: "spot", Effect: corev1.TaintEffectPreferNoSchedule},
	}
	n := FromNode(raw)
	want := []Taint{{"nvidia.com/gpu", "true", "NoSchedule"}, {"spot", "", "PreferNoSchedule"}}
	if !n.Unschedulable || !reflect.DeepEqual(n.Taints, want) {
		t.Fatalf("got %+v", n)
	}
}

func TestFromNodeConditions(t *testing.T) {
	n := FromNode(node(cond(corev1.NodeMemoryPressure, corev1.ConditionTrue), cond(corev1.NodeReady, corev1.ConditionUnknown)))
	if !n.Conditions["MemoryPressure"] || n.Conditions["DiskPressure"] || n.Conditions["Ready"] {
		t.Fatalf("got %+v", n.Conditions)
	}
	if silent := FromNode(node()); !silent.Conditions["Ready"] || silent.Conditions["MemoryPressure"] {
		t.Fatalf("no conditions must read as ready: %+v", silent.Conditions)
	}
}

func withLimits(c corev1.Container, cpu, mem string) corev1.Container {
	c.Resources.Limits = corev1.ResourceList{}
	if cpu != "" {
		c.Resources.Limits[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		c.Resources.Limits[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return c
}

func TestFromPodLimits(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	sidecar := withLimits(ctr("proxy", "50m", "32Mi"), "100m", "")
	sidecar.RestartPolicy = &always
	oneShot := ctr("migrate", "1", "1Gi") // plain init containers never bound the running pod

	cases := []struct {
		name     string
		pod      *corev1.Pod
		cpu, mem string // "" = unbounded
	}{
		{"all set", pod([]corev1.Container{withLimits(ctr("a", "", ""), "500m", "256Mi"), withLimits(ctr("b", "", ""), "250m", "128Mi")}), "750", "402653.184"},
		{"one container unbounded", pod([]corev1.Container{withLimits(ctr("a", "", ""), "500m", "256Mi"), ctr("b", "", "")}), "", ""},
		{"memory only", pod([]corev1.Container{withLimits(ctr("a", "", ""), "", "256Mi")}), "", "268435.456"},
		{"unbounded sidecar", pod([]corev1.Container{withLimits(ctr("a", "", ""), "500m", "256Mi")}, sidecar), "600", ""},
		{"plain init ignored", pod([]corev1.Container{withLimits(ctr("a", "", ""), "500m", "256Mi")}, oneShot), "500", "268435.456"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := FromPod(tc.pod)
			if got := fmtPtr(p.CPULimit); got != tc.cpu {
				t.Errorf("cpu limit = %q, want %q", got, tc.cpu)
			}
			if got := fmtPtr(p.MemoryLimit); got != tc.mem {
				t.Errorf("memory limit = %q, want %q", got, tc.mem)
			}
		})
	}
}

func TestFromPodPodLevelLimitBoundsThePod(t *testing.T) {
	p := pod([]corev1.Container{ctr("a", "100m", "")})
	p.Spec.Resources = &corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}
	if got := fmtPtr(FromPod(p).CPULimit); got != "2000" {
		t.Fatalf("cpu limit = %q", got)
	}
}

func fmtPtr[T int64 | float64](v *T) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(*v)
}

func TestFromNodeTopology(t *testing.T) {
	raw := node()
	raw.Labels = map[string]string{
		"topology.kubernetes.io/zone":      "eu-west-1a",
		"topology.kubernetes.io/region":    "eu-west-1",
		"node.kubernetes.io/instance-type": "m7g.xlarge",
		"eks.amazonaws.com/nodegroup":      "general",
	}
	n := FromNode(raw)
	if n.Zone != "eu-west-1a" || n.Region != "eu-west-1" || n.InstanceType != "m7g.xlarge" || n.Pool != "general" {
		t.Fatalf("got %+v", n)
	}
	raw.Labels = map[string]string{"karpenter.sh/nodepool": "spot", "eks.amazonaws.com/nodegroup": "general"}
	if n := FromNode(raw); n.Pool != "spot" || n.Zone != "" {
		t.Fatalf("karpenter pool wins, missing zone stays empty: %+v", n)
	}
}

func TestFromNodeCapacityType(t *testing.T) {
	cases := []struct {
		labels map[string]string
		want   string
	}{
		{map[string]string{"karpenter.sh/capacity-type": "spot"}, "spot"},
		{map[string]string{"karpenter.sh/capacity-type": "on-demand"}, "on-demand"},
		{map[string]string{"eks.amazonaws.com/capacityType": "SPOT"}, "spot"},
		{map[string]string{"eks.amazonaws.com/capacityType": "ON_DEMAND"}, "on-demand"},
		{map[string]string{"cloud.google.com/gke-spot": "true"}, "spot"},
		{map[string]string{"cloud.google.com/gke-provisioning": "spot"}, "spot"},
		{map[string]string{"kubernetes.azure.com/scalesetpriority": "spot"}, "spot"},
		{map[string]string{"kubernetes.azure.com/scalesetpriority": "regular"}, "on-demand"},
		// Karpenter wins, like the pool labels: it also runs on EKS nodes.
		{map[string]string{"karpenter.sh/capacity-type": "on-demand", "eks.amazonaws.com/capacityType": "SPOT"}, "on-demand"},
		{map[string]string{"karpenter.sh/capacity-type": "reserved"}, ""},
		{map[string]string{"cloud.google.com/gke-provisioning": "standard"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		raw := node()
		raw.Labels = tc.labels
		if got := FromNode(raw).CapacityType; got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.labels, got, tc.want)
		}
	}
}
