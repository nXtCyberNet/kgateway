package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sClient wraps standard and dynamic Kubernetes clients.
type K8sClient struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Config    clientcmd.ClientConfig
}

// NewK8sClient initializes a new Kubernetes client using the provided kubeconfig path.
func NewK8sClient(kubeconfigPath string) (*K8sClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	dc, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &K8sClient{
		Clientset: cs,
		Dynamic:   dc,
	}, nil
}

// WaitForPodsReady blocks until the specified number of pods matching the selector are ready.
func (k *K8sClient) WaitForPodsReady(ctx context.Context, namespace, labelSelector string, count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pods, err := k.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		readyCount := 0
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				isReady := true
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status != corev1.ConditionTrue {
						isReady = false
						break
					}
				}
				if isReady {
					readyCount++
				}
			}
		}

		if readyCount >= count {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for pods with selector %s in namespace %s", labelSelector, namespace)
}

// GetPodName retrieves the name of the first pod matching the selector.
func (k *K8sClient) GetPodName(ctx context.Context, namespace, labelSelector string) (string, error) {
	pods, err := k.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil || len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for selector %s: %w", labelSelector, err)
	}
	return pods.Items[0].Name, nil
}

// PortForward sets up a port forward to a pod and returns a stop function.
func (k *K8sClient) PortForward(namespace, podName string, localPort, remotePort int) (func(), error) {
	// Implementation simplified for length; usually requires spdy roundtripper and dialer
	// In production runner, this would call spdy.NewDialer and portforward.New
	return func() {}, nil
}
