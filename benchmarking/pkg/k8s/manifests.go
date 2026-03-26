package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

// ApplyManifestFile applies all resources defined in a YAML file.
func ApplyManifestFile(ctx context.Context, client *K8sClient, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	docs := strings.Split(string(data), "---")
	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		obj := &unstructured.Unstructured{}
		dec := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
		_, gvk, err := dec.Decode([]byte(doc), nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode manifest doc: %w", err)
		}

		// Map GVK to GVR for dynamic client
		dc, _ := discovery.NewDiscoveryClientForConfig(nil) // Simplified
		mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("failed to get REST mapping: %w", err)
		}

		var dr dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			dr = client.Dynamic.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		} else {
			dr = client.Dynamic.Resource(mapping.Resource)
		}

		_, err = dr.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{FieldManager: "kgateway-bench-runner", Force: true})
		if err != nil {
			return fmt.Errorf("failed to apply resource %s: %w", obj.GetName(), err)
		}
	}
	return nil
}

// ApplyManifestDir applies all .yaml files in a directory in alphabetical order.
func ApplyManifestDir(ctx context.Context, client *K8sClient, dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	var yamlFiles []string
	for _, f := range files {
		if !f.IsDir() && (strings.HasSuffix(f.Name(), ".yaml") || strings.HasSuffix(f.Name(), ".yml")) {
			yamlFiles = append(yamlFiles, filepath.Join(dir, f.Name()))
		}
	}
	sort.Strings(yamlFiles)

	for _, f := range yamlFiles {
		if err := ApplyManifestFile(ctx, client, f); err != nil {
			return err
		}
	}
	return nil
}
