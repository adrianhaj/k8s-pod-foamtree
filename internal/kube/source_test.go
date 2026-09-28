package kube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

const kubeconfig = `apiVersion: v1
kind: Config
current-context: kind-b
contexts:
- name: kind-b
  context: {cluster: c, user: u}
- name: kind-a
  context: {cluster: c, user: u}
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1"}
users:
- name: u
  user: {token: t}
`

func testSource(t *testing.T, cs kubernetes.Interface) *Source {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSource(false)
	s.rules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	s.newClient = func(string) (kubernetes.Interface, error) { return cs, nil }
	return s
}

func TestContextsSortedWithActiveFlag(t *testing.T) {
	got, err := testSource(t, fake.NewClientset()).Contexts()
	want := []Context{{"kind-a", false}, {"kind-b", true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestInClusterOffersOneContext(t *testing.T) {
	got, _ := NewSource(true).Contexts()
	if !reflect.DeepEqual(got, []Context{{InCluster, true}}) {
		t.Fatalf("got %v", got)
	}
}

func TestSnapshotReadsCache(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"}, Spec: corev1.PodSpec{NodeName: "n1",
			Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")}}}}}},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, pods, err := testSource(t, cs).Snapshot(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].CPU != 4000 || len(pods) != 1 || pods[0].CPU != 250 {
		t.Fatalf("nodes=%+v pods=%+v", nodes, pods)
	}
}

func TestSnapshotRejectsUnknownContext(t *testing.T) {
	_, _, err := testSource(t, fake.NewClientset()).Snapshot(context.Background(), "prod")
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("got %v", err)
	}
}

// An unreachable or unauthorised cluster must surface the API error, not hang.
func TestSnapshotReportsListError(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Unauthorized")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := testSource(t, cs).Snapshot(ctx, "kind-a")
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("got %v", err)
	}
}

// A failed first sync (bad kubeconfig) must not stick forever: fixing the
// kubeconfig and retrying should rebuild the cache from a fresh client.
func TestSnapshotRecoversAfterFailedFirstSync(t *testing.T) {
	bad := fake.NewClientset()
	bad.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Unauthorized")
	})
	good := fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	)
	s := testSource(t, bad)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := s.Snapshot(ctx, "kind-a"); err == nil {
		t.Fatal("want error on first snapshot")
	}
	s.newClient = func(string) (kubernetes.Interface, error) { return good, nil }
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	nodes, _, err := s.Snapshot(ctx2, "kind-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
}

// The fake clientset ignores field selectors, so this pins the request shape
// itself: dropping activePods would pass every other test silently.
func TestSnapshotFiltersPodsByFieldSelector(t *testing.T) {
	cs := fake.NewClientset()
	var got string
	cs.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		got = action.(k8stesting.ListAction).GetListRestrictions().Fields.String()
		return false, nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := testSource(t, cs).Snapshot(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got != fields.ParseSelectorOrDie(activePods).String() {
		t.Fatalf("got field selector %q, want %q", got, activePods)
	}
}

func TestSlimPodKeepsOnlyWhatTheDashboardReads(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	in := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", Labels: map[string]string{"app": "x"},
			Annotations: map[string]string{"big": "blob"}, ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}},
		Spec: corev1.PodSpec{NodeName: "n", Containers: []corev1.Container{{Name: "app", Image: "nginx",
			Env: []corev1.EnvVar{{Name: "SECRET", Value: "s3cr3t"}}}},
			InitContainers: []corev1.Container{{Name: "proxy", RestartPolicy: &always}}},
		Status: corev1.PodStatus{QOSClass: corev1.PodQOSBurstable, Message: "noise"},
	}
	out, _ := slimPod(in)
	p := out.(*corev1.Pod)
	if p.Annotations != nil || p.ManagedFields != nil || p.Spec.Containers[0].Env != nil || p.Spec.Containers[0].Image != "" || p.Status.Message != "" {
		t.Fatalf("not slimmed: %+v", p)
	}
	if p.Labels["app"] != "x" || p.Spec.NodeName != "n" || p.Status.QOSClass != corev1.PodQOSBurstable || *p.Spec.InitContainers[0].RestartPolicy != always {
		t.Fatalf("dropped a field the dashboard needs: %+v", p)
	}
}

func TestSlimNodeKeepsOnlyTopologyLabels(t *testing.T) {
	in := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		"topology.kubernetes.io/zone": "eu-west-1a", "karpenter.sh/nodepool": "spot",
		"kubernetes.io/hostname": "n", "beta.kubernetes.io/arch": "arm64",
	}}}
	out, _ := slimNode(in)
	want := map[string]string{"topology.kubernetes.io/zone": "eu-west-1a", "karpenter.sh/nodepool": "spot"}
	if got := out.(*corev1.Node).Labels; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
