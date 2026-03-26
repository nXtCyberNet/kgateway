package k8s

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type K8sClient struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Config    *rest.Config
}

func NewK8sClient(kubeconfigPath ...string) (*K8sClient, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if len(kubeconfigPath) > 0 && kubeconfigPath[0] != "" {
		loadingRules.ExplicitPath = kubeconfigPath[0]
	}
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &K8sClient{
		Clientset: cs,
		Dynamic:   dyn,
		Config:    config,
	}, nil
}

func (c *K8sClient) HelmInstall(release, chartPath, ns string, values map[string]interface{}) error {
	args := []string{"upgrade", "-i", release, chartPath, "-n", ns, "--create-namespace", "--wait", "--timeout", "5m"}
	if len(values) > 0 {
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "--set", fmt.Sprintf("%s=%v", k, values[k]))
		}
	}
	cmd := exec.Command("helm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("helm install/upgrade failed: %v", err)
		return nil
	}
	return nil
}

func (c *K8sClient) HelmUninstall(release, ns string) error {
	cmd := exec.Command("helm", "uninstall", release, "-n", ns, "--wait")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("helm uninstall failed: %v", err)
		return nil
	}
	return nil
}

func (c *K8sClient) WaitForJobCompletion(ctx context.Context, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			log.Fatalf("timeout waiting for job %s/%s", ns, name)
			return nil
		default:
			job, err := c.Clientset.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if job.Status.Succeeded > 0 {
				return nil
			}
			if job.Status.Failed > 0 {
				log.Fatalf("job %s failed", name)
				return nil
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (c *K8sClient) PortForward(ns, podName string, localPort, podPort int) (func(), error) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(c.Config)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", ns, podName)
	hostIP := strings.TrimLeft(c.Config.Host, "https://")
	serverURL := &url.URL{Scheme: "https", Path: path, Host: hostIP}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, serverURL)
	stopChan, readyChan := make(chan struct{}, 1), make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, podPort)}, stopChan, readyChan, nil, nil)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := fw.ForwardPorts(); err != nil {
			panic(err)
		}
	}()

	<-readyChan
	return func() { close(stopChan) }, nil
}
