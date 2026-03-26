package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

var decUnstructured = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

// ApplyManifestDir applies all .yaml/.yml files in a directory in alphabetical order.
func (c *K8sClient) ApplyManifestDir(ctx context.Context, dir string) error {
	return c.handleDir(ctx, dir, true)
}

// DeleteManifestDir deletes all resources defined in .yaml/.yml files in a directory.
func (c *K8sClient) DeleteManifestDir(ctx context.Context, dir string) error {
	return c.handleDir(ctx, dir, false)
}

func (c *K8sClient) handleDir(ctx context.Context, dir string, apply bool) error {
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
		if err := c.handleFile(ctx, f, apply); err != nil {
			return err
		}
	}
	return nil
}

func (c *K8sClient) handleFile(ctx context.Context, path string, apply bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read manifest %s: %w", path, err)
	}

	// Setup discovery and mapper using the actual config
	dc, err := discovery.NewDiscoveryClientForConfig(c.Config)
	if err != nil {
		return err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		obj := &unstructured.Unstructured{}
		_, gvk, err := decUnstructured.Decode(rawObj.Raw, nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode object: %w", err)
		}

		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("failed to get REST mapping for %v: %w", gvk, err)
		}

		var dr dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			dr = c.Dynamic.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		} else {
			dr = c.Dynamic.Resource(mapping.Resource)
		}

		if apply {
			_, err = dr.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{
				FieldManager: "kgateway-bench-runner",
				Force:        true,
			})
		} else {
			err = dr.Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
		}

		if err != nil {
			return fmt.Errorf("operation (apply=%v) failed for %s: %w", apply, obj.GetName(), err)
		}
	}

	return nil
}
