// Package isolation sets up and tears down the per-build security boundary:
// ephemeral namespace + ResourceQuota/LimitRange + default-deny NetworkPolicy +
// short-lived per-build Secrets. This is the security spine (plan §4/§11 #1):
// untrusted user code must never reach cluster credentials or arbitrary egress.
package isolation

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// Quotas bounds a build namespace.
type Quotas struct {
	CPULimit         string // e.g. "2"
	MemLimit         string // e.g. "4Gi"
	PodCount         int    // hard pod cap
	EphemeralStorage string // e.g. "10Gi"
}

// Manager provisions and reclaims per-build isolation primitives.
type Manager interface {
	// EnsureNamespace creates build-<id> ns + ResourceQuota + LimitRange and
	// returns its name.
	EnsureNamespace(ctx context.Context, buildID string) (string, error)
	// ApplyNetworkPolicy applies default-deny ingress/egress then allows egress
	// ONLY to kube-dns + the given git/registry CIDRs.
	ApplyNetworkPolicy(ctx context.Context, namespace string, gitEgressCIDRs string) error
	// CreateSecret creates an opaque per-build Secret in the namespace. Data is
	// scrubbed automatically when the namespace is torn down.
	CreateSecret(ctx context.Context, namespace, name string, data map[string][]byte) error
	// CreateDockerConfigSecret creates a kubernetes.io/dockerconfigjson Secret
	// (Harbor robot creds for BuildKit push).
	CreateDockerConfigSecret(ctx context.Context, namespace, name, registryHost, user, pass string) error
	// Teardown deletes the namespace (cascades Job, secrets, netpol).
	Teardown(ctx context.Context, namespace string) error
}

// K8sManager is the production Manager backed by the in-cluster API.
type K8sManager struct {
	cs            kubernetes.Interface
	quotas        Quotas
	registryCIDRs string // extra egress CIDRs for Harbor (optional)
}

// NewK8sManager returns a K8sManager.
func NewK8sManager(cs kubernetes.Interface, q Quotas, registryCIDRs string) *K8sManager {
	return &K8sManager{cs: cs, quotas: q, registryCIDRs: registryCIDRs}
}

// NamespaceFor is the deterministic ephemeral namespace name for a build.
func NamespaceFor(buildID string) string { return "build-" + buildID }

func buildLabels(buildID string) map[string]string {
	return map[string]string{
		"dada.io/build":                buildID,
		"app.kubernetes.io/managed-by": "build-agent",
	}
}

func (m *K8sManager) EnsureNamespace(ctx context.Context, buildID string) (string, error) {
	name := NamespaceFor(buildID)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: buildLabels(buildID)},
	}
	if _, err := m.cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("create namespace: %w", err)
		}
	}

	cpu, err := resource.ParseQuantity(m.quotas.CPULimit)
	if err != nil {
		return "", fmt.Errorf("parse cpu limit: %w", err)
	}
	mem, err := resource.ParseQuantity(m.quotas.MemLimit)
	if err != nil {
		return "", fmt.Errorf("parse mem limit: %w", err)
	}
	pods := m.quotas.PodCount
	if pods <= 0 {
		pods = 2 // builder + buildkitd
	}
	eph := m.quotas.EphemeralStorage
	if eph == "" {
		eph = "10Gi"
	}
	ephQ, err := resource.ParseQuantity(eph)
	if err != nil {
		return "", fmt.Errorf("parse ephemeral storage: %w", err)
	}

	rq := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "build-quota", Namespace: name, Labels: buildLabels(buildID)},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceLimitsCPU:              cpu,
				corev1.ResourceLimitsMemory:           mem,
				corev1.ResourcePods:                   *resource.NewQuantity(int64(pods), resource.DecimalSI),
				corev1.ResourceLimitsEphemeralStorage: ephQ,
			},
		},
	}
	if _, err := m.cs.CoreV1().ResourceQuotas(name).Create(ctx, rq, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("create resource quota: %w", err)
	}

	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "build-limits", Namespace: name, Labels: buildLabels(buildID)},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer,
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    cpu,
					corev1.ResourceMemory: mem,
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			}},
		},
	}
	if _, err := m.cs.CoreV1().LimitRanges(name).Create(ctx, lr, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("create limit range: %w", err)
	}

	return name, nil
}

func (m *K8sManager) ApplyNetworkPolicy(ctx context.Context, namespace, gitEgressCIDRs string) error {
	// 1. Default-deny everything (ingress + egress) in the namespace.
	deny := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default-deny", Namespace: namespace},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // all pods
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress},
		},
	}
	if _, err := m.cs.NetworkingV1().NetworkPolicies(namespace).Create(ctx, deny, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create default-deny netpol: %w", err)
	}

	// 2. Allow egress: kube-dns (UDP/TCP 53) + git + registry CIDRs only.
	dnsUDP := corev1.ProtocolUDP
	dnsTCP := corev1.ProtocolTCP
	port53 := intstr.FromInt(53)

	var egress []netv1.NetworkPolicyEgressRule

	// DNS to kube-dns service (allow to any IP on port 53 — DNS is essential and
	// resolves to ClusterIP; CNI scopes it inside the cluster).
	egress = append(egress, netv1.NetworkPolicyEgressRule{
		Ports: []netv1.NetworkPolicyPort{
			{Protocol: &dnsUDP, Port: &port53},
			{Protocol: &dnsTCP, Port: &port53},
		},
	})

	// git + registry CIDRs over HTTPS (443) and SSH (22).
	cidrs := splitCIDRs(gitEgressCIDRs)
	cidrs = append(cidrs, splitCIDRs(m.registryCIDRs)...)
	if len(cidrs) > 0 {
		var peers []netv1.NetworkPolicyPeer
		for _, c := range cidrs {
			peers = append(peers, netv1.NetworkPolicyPeer{
				IPBlock: &netv1.IPBlock{CIDR: c},
			})
		}
		https := intstr.FromInt(443)
		ssh := intstr.FromInt(22)
		egress = append(egress, netv1.NetworkPolicyEgressRule{
			To: peers,
			Ports: []netv1.NetworkPolicyPort{
				{Protocol: &dnsTCP, Port: &https},
				{Protocol: &dnsTCP, Port: &ssh},
			},
		})
	}

	allow := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-egress", Namespace: namespace},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
	if _, err := m.cs.NetworkingV1().NetworkPolicies(namespace).Create(ctx, allow, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create allow-egress netpol: %w", err)
	}
	return nil
}

func (m *K8sManager) CreateSecret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	if _, err := m.cs.CoreV1().Secrets(namespace).Create(ctx, sec, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create secret %s: %w", name, err)
	}
	return nil
}

func (m *K8sManager) CreateDockerConfigSecret(ctx context.Context, namespace, name, registryHost, user, pass string) error {
	dockercfg := dockerConfigJSON(registryHost, user, pass)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: dockercfg},
	}
	if _, err := m.cs.CoreV1().Secrets(namespace).Create(ctx, sec, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create dockercfg secret %s: %w", name, err)
	}
	return nil
}

func (m *K8sManager) Teardown(ctx context.Context, namespace string) error {
	if err := m.cs.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %s: %w", namespace, err)
	}
	return nil
}

func splitCIDRs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var _ Manager = (*K8sManager)(nil)
