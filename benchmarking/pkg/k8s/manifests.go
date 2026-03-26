package k8s

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

// ApplyManifestFile reads YAML, splits by "---", and applies each document using the dynamic client
func ApplyManifestFile(ctx context.Context, client *K8sClient, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}

	return processSplitYAML(ctx, client, data, false)
}

// DeleteManifestFile reads YAML, splits by "---", and deletes each document
func DeleteManifestFile(ctx context.Context, client *K8sClient, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}

	return processSplitYAML(ctx, client, data, true)
}

// ApplyManifestDir applies all .yaml files in a directory in alphabetical order
func ApplyManifestDir(ctx context.Context, client *K8sClient, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read manifest dir %s: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)

	for _, file := range files {
		if err := ApplyManifestFile(ctx, client, file); err != nil {
			return fmt.Errorf("apply manifest file %s: %w", file, err)
		}
	}
	return nil
}

// processSplitYAML orchestrates splitting the YAML stream and processing each document.
func processSplitYAML(ctx context.Context, client *K8sClient, data []byte, isDelete bool) error {
	mapper, err := buildRESTMapper(client)
	if err != nil {
		return err
	}

	parts := bytes.Split(data, []byte("\n---"))
	for _, part := range parts {
		if err := processSingleDocument(ctx, client, mapper, part, isDelete); err != nil {
			return err
		}
	}
	return nil
}

// buildRESTMapper constructs a deferred discovery REST mapper for dynamic API resolution
func buildRESTMapper(client *K8sClient) (meta.RESTMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(client.Config)
	if err != nil {
		return nil, fmt.Errorf("create discovery client: %w", err)
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc)), nil
}

// processSingleDocument parses and executes apply/delete against a single unmarshaled manifest structure
func processSingleDocument(ctx context.Context, client *K8sClient, mapper meta.RESTMapper, part []byte, isDelete bool) error {
	if len(bytes.TrimSpace(part)) == 0 {
		return nil
	}

	var unstruct unstructured.Unstructured
	if err := yaml.Unmarshal(part, &unstruct); err != nil {
		return fmt.Errorf("unmarshal yaml: %w", err)
	}

	if unstruct.Object == nil {
		return nil
	}

	resInterface, err := resolveResourceInterface(client, mapper, &unstruct)
	if err != nil {
		return err
	}

	if isDelete {
		return deleteResource(ctx, resInterface, unstruct.GetName())
	}
	return applyResource(ctx, resInterface, &unstruct)
}

// resolveResourceInterface determines the dynamic resource endpoint given GVK and metadata
func resolveResourceInterface(client *K8sClient, mapper meta.RESTMapper, unstruct *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	gvk := unstruct.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("get REST mapping for %v: %w", gvk, err)
	}

	namespace := unstruct.GetNamespace()
	if namespace == "" && mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		unstruct.SetNamespace("default")
		namespace = "default"
	}

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return client.Dynamic.Resource(mapping.Resource).Namespace(namespace), nil
	}
	return client.Dynamic.Resource(mapping.Resource), nil
}

// applyResource either creates or force-updates the provided unstructured object
func applyResource(ctx context.Context, resInterface dynamic.ResourceInterface, unstruct *unstructured.Unstructured) error {
	name := unstruct.GetName()
	_, err := resInterface.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if _, createErr := resInterface.Create(ctx, unstruct, metav1.CreateOptions{}); createErr != nil {
			return fmt.Errorf("create resource %s: %w", name, createErr)
		}
		return nil
	}

	unstruct.SetResourceVersion("") // force overwrite
	if _, updateErr := resInterface.Update(ctx, unstruct, metav1.UpdateOptions{}); updateErr != nil {
		return fmt.Errorf("update resource %s: %w", name, updateErr)
	}
	return nil
}

// deleteResource deletes a named resource ignoring NotFound errors
func deleteResource(ctx context.Context, resInterface dynamic.ResourceInterface, name string) error {
	err := resInterface.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("delete resource %s: %w", name, err)
	}
	return nil
}
