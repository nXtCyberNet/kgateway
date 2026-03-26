package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// K8sClient wraps strong-typed and dynamic kubernetes clients
type K8sClient struct {
	Clientset *kubernetes.Clientset
	Dynamic   dynamic.Interface
	Config    *rest.Config
}

// NewK8sClient initializes a K8sClient using kubeconfig
func NewK8sClient(kubeconfig string) (*K8sClient, error) {
	cfg, err := resolveKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("resolve kubeconfig: %w", err)
	}

	config, err := clientcmd.BuildConfigFromFlags("", cfg)
	if err != nil {
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return &K8sClient{
		Clientset: clientset,
		Dynamic:   dynClient,
		Config:    config,
	}, nil
}

// resolveKubeConfig finds the correct local path for the kubernetes config file
func resolveKubeConfig(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		return envPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home dir: %w", err)
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// WaitForPodsReady blocks until "count" pods matching labelSelector are running 
func (k *K8sClient) WaitForPodsReady(ctx context.Context, namespace, labelSelector string, count int, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		return k.checkPodReadyCondition(ctx, namespace, labelSelector, count)
	})
	if err != nil {
		return fmt.Errorf("wait for pods ready condition failed over %v: %w", timeout, err)
	}
	return nil
}

// checkPodReadyCondition counts exactly how many pods matching the given selector are explicitly Ready
func (k *K8sClient) checkPodReadyCondition(ctx context.Context, namespace, labelSelector string, targetCount int) (bool, error) {
	pods, err := k.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return false, err
	}

	readyCount := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning && isPodReadyConditionTrue(&pod) {
			readyCount++
		}
	}

	return readyCount >= targetCount, nil
}

// isPodReadyConditionTrue extracts internal boolean from corev1 PodStatus
func isPodReadyConditionTrue(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// GetPodName retrieves the name of the first pod matching the selector
func (k *K8sClient) GetPodName(ctx context.Context, namespace, labelSelector string) (string, error) {
	pods, err := k.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found matching selector %s", labelSelector)
	}
	return pods.Items[0].Name, nil
}

// PortForward initiates a port forward to a specific pod and returns a closure function to safely stop it later
func (k *K8sClient) PortForward(namespace, podName string, localPort, remotePort int) (func(), error) {
	req := k.Clientset.RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward")

	dialer, err := k.buildSPDYDialer(req)
	if err != nil {
		return nil, err
	}

	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, remotePort)}, stopCh, readyCh, os.Stdout, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create portforward container stream: %w", err)
	}

	go executeForwardingRoutine(fw)

	if err := waitForPortForwardReady(readyCh, stopCh); err != nil {
		return nil, err
	}

	return func() { close(stopCh) }, nil
}

// buildSPDYDialer negotiates standard authenticated streams across Kube apiserver connections
func (k *K8sClient) buildSPDYDialer(req *rest.Request) (httpstream.Dialer, error) {
	transport, upgrader, err := spdy.RoundTripperFor(k.Config)
	if err != nil {
		return nil, fmt.Errorf("create spdy framework roundtripper: %w", err)
	}
	
	dialerURL := &url.URL{Scheme: "https", Path: req.URL().Path, RawQuery: req.URL().RawQuery}
	return spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, dialerURL), nil
}

// executeForwardingRoutine serves as a background handler
func executeForwardingRoutine(fw *portforward.PortForwarder) {
	if err := fw.ForwardPorts(); err != nil {
		fmt.Printf("portforward handler failed dynamically: %v\n", err)
	}
}

// waitForPortForwardReady blocks synchronously up to a limit for readiness handshake
func waitForPortForwardReady(readyCh, stopCh chan struct{}) error {
	select {
	case <-readyCh:
		return nil
	case <-time.After(5 * time.Second):
		close(stopCh)
		return fmt.Errorf("timed out waiting for portforward handshake hook")
	}
}
