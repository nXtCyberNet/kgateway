package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// K8sClient wraps client-go clientset and dynamic client for all benchmark orchestration.
type K8sClient struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Config    *rest.Config
}

// NewK8sClient builds a K8sClient from the given kubeconfig path.
// If kubeconfig is empty, it falls back to in-cluster config.
func NewK8sClient(kubeconfig string) (*K8sClient, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, &clientcmd.ConfigOverrides{},
		)
		config, err = kubeConfig.ClientConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &K8sClient{Clientset: cs, Dynamic: dyn, Config: config}, nil
}

// WaitForPodsReady polls until at least count pods matching labelSelector in namespace
// are all in Running/Ready state, or until timeout elapses.
func (c *K8sClient) WaitForPodsReady(ctx context.Context, namespace, labelSelector string, count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastPods []corev1.Pod
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for pods: %w", ctx.Err())
		default:
		}

		pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list pods in %s: %w", namespace, err)
		}
		lastPods = pods.Items

		ready := 0
		for _, pod := range pods.Items {
			if isPodReady(&pod) {
				ready++
			}
		}
		if ready >= count {
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	if len(lastPods) == 0 {
		return fmt.Errorf("timed out after %s waiting for %d pods with selector %q in %s (no pods found)",
			timeout, count, labelSelector, namespace)
	}

	var details strings.Builder
	for _, pod := range lastPods {
		details.WriteString(fmt.Sprintf("%s phase=%s", pod.Name, pod.Status.Phase))
		if pod.Spec.NodeName != "" {
			details.WriteString(fmt.Sprintf(" node=%s", pod.Spec.NodeName))
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				details.WriteString(fmt.Sprintf(" container=%s waiting=%s(%s)", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
			}
			if cs.State.Terminated != nil {
				details.WriteString(fmt.Sprintf(" container=%s terminated=%s(%s)", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.Message))
			}
		}
		details.WriteString("; ")
	}

	return fmt.Errorf("timed out after %s waiting for %d pods with selector %q in %s; pod details: %s",
		timeout, count, labelSelector, namespace, strings.TrimSpace(details.String()))
}

// GetPodName returns the name of the first Running pod matching labelSelector in namespace.
func (c *K8sClient) GetPodName(ctx context.Context, namespace, labelSelector string) (string, error) {
	pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods in %s: %w", namespace, err)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name, nil
		}
	}

	return "", fmt.Errorf("no running pod found with selector %q in %s", labelSelector, namespace)
}

// PortForward opens a port-forward tunnel to podName in namespace from localPort to remotePort.
// It returns a stop function that tears down the tunnel. The stop function is safe to call
// multiple times thanks to sync.Once.
func (c *K8sClient) PortForward(ctx context.Context, namespace, podName string, localPort, remotePort int) (func(), error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName)
	hostURL, err := url.Parse(c.Config.Host + path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse port-forward URL: %w", err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(c.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY round-tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, hostURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	fw, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stopCh,
		readyCh,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	// Wait for tunnel to be ready or fail.
	select {
	case <-readyCh:
	case err := <-errCh:
		return nil, fmt.Errorf("port-forward failed to start: %w", err)
	case <-time.After(30 * time.Second):
		close(stopCh)
		return nil, fmt.Errorf("timed out waiting for port-forward to become ready")
	case <-ctx.Done():
		close(stopCh)
		return nil, fmt.Errorf("context cancelled before port-forward ready: %w", ctx.Err())
	}

	// sync.Once ensures stopCh is closed exactly once regardless of how many
	// callers invoke the stop function or whether ForwardPorts closes it internally.
	var once sync.Once
	stop := func() {
		once.Do(func() { close(stopCh) })
	}

	return stop, nil
}

// HelmInstall runs helm upgrade --install for the given release and chart.
// values is marshalled to a temporary YAML file so nested structures are preserved.
func (c *K8sClient) HelmInstall(ctx context.Context, release, chartPath, ns string, values map[string]interface{}) error {
	tmpFile, err := os.CreateTemp("", "helm-values-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp values file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	data, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal helm values: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed to write helm values: %w", err)
	}
	tmpFile.Close()

	args := []string{
		"upgrade", "--install", release, chartPath,
		"--namespace", ns,
		"--create-namespace",
		"--values", tmpFile.Name(),
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm install %q failed: %w (stderr: %s)", release, err, stderr.String())
	}

	return nil
}

// HelmUninstall removes a Helm release. A "not found" error is treated as a no-op.
func (c *K8sClient) HelmUninstall(ctx context.Context, release, ns string) error {
	args := []string{"uninstall", release, "--namespace", ns, "--wait"}

	cmd := exec.CommandContext(ctx, "helm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "not found") {
			return nil
		}
		return fmt.Errorf("helm uninstall %q failed: %w (stderr: %s)", release, err, stderr.String())
	}

	return nil
}

// WaitForJobComplete polls until the named Job in namespace reaches a terminal state
// (Complete or Failed), or until timeout elapses.
func (c *K8sClient) WaitForJobComplete(ctx context.Context, namespace, jobName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastPodDetails := ""
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for job %s: %w", jobName, ctx.Err())
		default:
		}

		job, err := c.Clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				time.Sleep(2 * time.Second)
				continue
			}
			return fmt.Errorf("failed to get job %s: %w", jobName, err)
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return fmt.Errorf("job %s failed: %s", jobName, cond.Message)
			}
		}

		pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("job-name=%s", jobName),
		})
		if err == nil {
			details, fatalReason := summarizeJobPods(pods.Items)
			if details != "" {
				lastPodDetails = details
			}
			if fatalReason != "" {
				return fmt.Errorf("job %s cannot make progress: %s; pod details: %s", jobName, fatalReason, lastPodDetails)
			}
		}

		time.Sleep(10 * time.Second)
	}

	if lastPodDetails != "" {
		return fmt.Errorf("timed out after %s waiting for job %s to complete; pod details: %s", timeout, jobName, lastPodDetails)
	}

	return fmt.Errorf("timed out after %s waiting for job %s to complete", timeout, jobName)
}

func summarizeJobPods(pods []corev1.Pod) (string, string) {
	if len(pods) == 0 {
		return "no job pod created yet", ""
	}

	fatalWaitReasons := map[string]bool{
		"ErrImagePull":               true,
		"ImagePullBackOff":           true,
		"InvalidImageName":           true,
		"CreateContainerConfigError": true,
		"CreateContainerError":       true,
	}

	var details strings.Builder
	var fatalReason string

	for _, pod := range pods {
		details.WriteString(fmt.Sprintf("%s phase=%s", pod.Name, pod.Status.Phase))

		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				msg := cs.State.Waiting.Message
				details.WriteString(fmt.Sprintf(" init=%s waiting=%s(%s)", cs.Name, reason, msg))
				if fatalWaitReasons[reason] && fatalReason == "" {
					fatalReason = fmt.Sprintf("init container %s is waiting with %s", cs.Name, reason)
				}
			}
			if cs.State.Terminated != nil {
				details.WriteString(fmt.Sprintf(" init=%s terminated=%s(%s)", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.Message))
			}
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				msg := cs.State.Waiting.Message
				details.WriteString(fmt.Sprintf(" container=%s waiting=%s(%s)", cs.Name, reason, msg))
				if fatalWaitReasons[reason] && fatalReason == "" {
					fatalReason = fmt.Sprintf("container %s is waiting with %s", cs.Name, reason)
				}
			}
			if cs.State.Terminated != nil {
				term := cs.State.Terminated
				details.WriteString(fmt.Sprintf(" container=%s terminated=%s(exit=%d)", cs.Name, term.Reason, term.ExitCode))
				if term.ExitCode != 0 && fatalReason == "" {
					fatalReason = fmt.Sprintf("container %s terminated with %s (exit %d)", cs.Name, term.Reason, term.ExitCode)
				}
			}
		}

		details.WriteString("; ")
	}

	return strings.TrimSpace(details.String()), fatalReason
}

// GetJobLogs returns the combined stdout logs from all pods belonging to the named Job.
func (c *K8sClient) GetJobLogs(ctx context.Context, namespace, jobName string) (string, error) {
	pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for job %s: %w", jobName, err)
	}

	var combined strings.Builder
	for _, pod := range pods.Items {
		req := c.Clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		stream, err := req.Stream(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to stream logs for pod %s: %w", pod.Name, err)
		}
		defer stream.Close()

		data, err := io.ReadAll(stream)
		if err != nil {
			return "", fmt.Errorf("failed to read logs for pod %s: %w", pod.Name, err)
		}
		combined.WriteString(string(data))
	}

	return combined.String(), nil
}

// isPodReady returns true when all containers in the pod have passed their readiness checks.
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
