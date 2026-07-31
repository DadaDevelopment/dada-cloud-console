package box

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// ClusterExposer publishes one port of a box through the cluster's nginx ingress,
// on a hostname the PLATFORM assigns under a wildcard.
//
// The wildcard certificate is the reason this is not per-box Let's Encrypt: a
// certificate per hostname would hit the 50-per-domain-per-week ceiling, which
// means 50 boxes a week would end the product. One DNS-01 wildcard, replicated
// into the box namespace, serves every box that will ever exist.
//
// Exposing also LABELS the box pod, because the namespace default-denies ingress
// and only the box-ingress-from-nginx policy lets the edge in. A box is therefore
// unreachable from the internet until someone exposes a port on purpose, and
// becomes unreachable again the moment the label goes.
//
// Fields:
//
//   - Namespace is where box pods, Services and Ingresses live.
//   - HostnameBase is the platform wildcard assigned hostnames live under.
//   - TLSSecret is the replicated wildcard certificate in Namespace.
//   - IngressClass selects the public ingress controller.
type ClusterExposer struct {
	clientset kubernetes.Interface

	Namespace    string
	HostnameBase string
	TLSSecret    string
	IngressClass string
}

// labelBoxName ties a Service selector to a box the control plane knows by name.
// The pod's own id is a pool-internal handle that changes with every claim, so a
// selector built on it would break the moment a box was rebound.
const labelBoxName = "dada.io/box-name"

// labelBoxExposed is what the box-ingress-from-nginx NetworkPolicy selects.
const labelBoxExposed = "dada.io/box-exposed"

var _ Exposer = (*ClusterExposer)(nil)

// NewClusterExposer builds an exposer sharing the runtime's API client, so both
// speak to the cluster as the same service account and an RBAC gap shows up in
// one place rather than two.
func NewClusterExposer(rt *ClusterRuntime, hostnameBase string) *ClusterExposer {
	return &ClusterExposer{
		clientset:    rt.clientset,
		Namespace:    rt.Namespace,
		HostnameBase: hostnameBase,
		TLSSecret:    "box-wildcard-tls",
		IngressClass: "nginx",
	}
}

// exposureName is the Service and Ingress name for a box port. Derived rather
// than stored so Unexpose can find both objects from the hostname alone, which is
// all the control plane records.
func exposureName(boxName string, port int) string {
	return fmt.Sprintf("box-%s-%d", boxName, port)
}

// Expose publishes port on a platform-assigned hostname and opens the ingress
// path to the box. It is idempotent: re-exposing the same port updates the
// objects instead of failing, because a retried request must not leave a box
// half-published.
func (e *ClusterExposer) Expose(boxName string, port int) (Exposure, error) {
	if port <= 0 || port > 65535 {
		return Exposure{}, fmt.Errorf("box: refusing to expose port %d", port)
	}
	ctx := context.Background()
	name := exposureName(boxName, port)
	hostname := fmt.Sprintf("%s-%d.%s", boxName, port, e.HostnameBase)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: e.Namespace,
			Labels:    map[string]string{labelBox: "true", labelBoxName: boxName},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelBoxName: boxName},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       80,
				TargetPort: intstr.FromInt32(int32(port)),
			}},
		},
	}
	if err := e.applyService(ctx, svc); err != nil {
		return Exposure{}, err
	}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: e.Namespace,
			Labels:    map[string]string{labelBox: "true", labelBoxName: boxName},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
				"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
				"nginx.ingress.kubernetes.io/proxy-body-size":    "64m",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(e.IngressClass),
			TLS:              []networkingv1.IngressTLS{{Hosts: []string{hostname}, SecretName: e.TLSSecret}},
			Rules: []networkingv1.IngressRule{{
				Host: hostname,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if err := e.applyIngress(ctx, ing); err != nil {
		return Exposure{}, err
	}
	if err := e.setExposedLabel(ctx, boxName, true); err != nil {
		return Exposure{}, err
	}

	return Exposure{
		Hostname: hostname,
		Port:     port,
		EdgePort: 443,
		URL:      "https://" + hostname,
		Cert:     e.TLSSecret,
	}, nil
}

// Unexpose removes the published hostname. The box keeps running: unexposing is
// about reachability, not about the body.
func (e *ClusterExposer) Unexpose(hostname string) error {
	ctx := context.Background()
	boxName, port, err := splitExposedHostname(hostname, e.HostnameBase)
	if err != nil {
		return err
	}
	name := exposureName(boxName, port)

	if err := e.clientset.NetworkingV1().Ingresses(e.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete box ingress: %w", err)
	}
	if err := e.clientset.CoreV1().Services(e.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete box service: %w", err)
	}

	remaining, err := e.clientset.NetworkingV1().Ingresses(e.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBoxName + "=" + boxName,
	})
	if err != nil {
		return fmt.Errorf("list box ingresses: %w", err)
	}
	if len(remaining.Items) == 0 {
		return e.setExposedLabel(ctx, boxName, false)
	}
	return nil
}

// splitExposedHostname recovers the box and port from an assigned hostname. The
// control plane stores only the hostname, so this is what makes Unexpose possible
// without a second table that could disagree with the cluster.
func splitExposedHostname(hostname, base string) (string, int, error) {
	suffix := "." + strings.TrimPrefix(base, ".")
	if !strings.HasSuffix(hostname, suffix) {
		return "", 0, fmt.Errorf("box: hostname %q is not under %q", hostname, base)
	}
	head := strings.TrimSuffix(hostname, suffix)
	idx := strings.LastIndex(head, "-")
	if idx <= 0 || idx == len(head)-1 {
		return "", 0, fmt.Errorf("box: hostname %q does not name a box port", hostname)
	}
	var port int
	if _, err := fmt.Sscanf(head[idx+1:], "%d", &port); err != nil || port <= 0 {
		return "", 0, fmt.Errorf("box: hostname %q does not name a box port", hostname)
	}
	return head[:idx], port, nil
}

// setExposedLabel adds or removes the label the ingress NetworkPolicy selects.
// The label is the reachability switch, so it is set AFTER the Service exists and
// cleared as soon as the last exposure goes.
func (e *ClusterExposer) setExposedLabel(ctx context.Context, boxName string, exposed bool) error {
	pods, err := e.clientset.CoreV1().Pods(e.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBoxName + "=" + boxName,
	})
	if err != nil {
		return fmt.Errorf("list box pods: %w", err)
	}
	value := "null"
	if exposed {
		value = `"true"`
	}
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%s}}}`, labelBoxExposed, value)
	for i := range pods.Items {
		_, err := e.clientset.CoreV1().Pods(e.Namespace).Patch(ctx, pods.Items[i].Name,
			types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("label box pod %s: %w", pods.Items[i].Name, err)
		}
	}
	return nil
}

// applyService creates the Service or updates the existing one in place. Update
// rather than delete-and-recreate: a recreated Service gets a new ClusterIP, and
// an in-flight request through the edge would break for no reason.
func (e *ClusterExposer) applyService(ctx context.Context, svc *corev1.Service) error {
	existing, err := e.clientset.CoreV1().Services(e.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := e.clientset.CoreV1().Services(e.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create box service: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get box service: %w", err)
	}
	existing.Labels = svc.Labels
	existing.Spec.Selector = svc.Spec.Selector
	existing.Spec.Ports = svc.Spec.Ports
	if _, err := e.clientset.CoreV1().Services(e.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update box service: %w", err)
	}
	return nil
}

// applyIngress creates or updates the Ingress under the same idempotence rule as
// applyService.
func (e *ClusterExposer) applyIngress(ctx context.Context, ing *networkingv1.Ingress) error {
	existing, err := e.clientset.NetworkingV1().Ingresses(e.Namespace).Get(ctx, ing.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := e.clientset.NetworkingV1().Ingresses(e.Namespace).Create(ctx, ing, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create box ingress: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get box ingress: %w", err)
	}
	existing.Labels = ing.Labels
	existing.Annotations = ing.Annotations
	existing.Spec = ing.Spec
	if _, err := e.clientset.NetworkingV1().Ingresses(e.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update box ingress: %w", err)
	}
	return nil
}
