// pkg/k8s/manifests.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

// ApplyManifestFile applies all Kubernetes resources in a single YAML file using
// Server-Side Apply. Multi-document YAML (separated by ---) is fully supported.
func (c *K8sClient) ApplyManifestFile(ctx context.Context, path string) error {
	mapper, err := c.buildRESTMapper()
	if err != nil {
		return fmt.Errorf("failed to build REST mapper: %w", err)
	}
	return c.processFile(ctx, path, mapper, true)
}

// DeleteManifestFile deletes all Kubernetes resources declared in a single YAML file.
// Resources that are already absent are silently skipped.
func (c *K8sClient) DeleteManifestFile(ctx context.Context, path string) error {
	mapper, err := c.buildRESTMapper()
	if err != nil {
		return fmt.Errorf("failed to build REST mapper: %w", err)
	}
	return c.processFile(ctx, path, mapper, false)
}

// ApplyManifestDir applies all *.yaml files in dir in alphabetical order.
// The REST mapper is built once and reused across all files.
func (c *K8sClient) ApplyManifestDir(ctx context.Context, dir string) error {
	return c.handleDir(ctx, dir, true)
}

// DeleteManifestDir deletes all resources declared by *.yaml files in dir.
// Files are processed in reverse alphabetical order so that child resources
// (e.g. HTTPRoutes) are removed before their parents (Gateways), preventing
// finalizer deadlocks.
func (c *K8sClient) DeleteManifestDir(ctx context.Context, dir string) error {
	return c.handleDir(ctx, dir, false)
}

// handleDir is the shared implementation for ApplyManifestDir and DeleteManifestDir.
// The REST mapper is built once here and passed down to avoid one API round-trip per file.
func (c *K8sClient) handleDir(ctx context.Context, dir string, apply bool) error {
	files, err := yamlFilesInDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read manifest dir %s: %w", dir, err)
	}

	if !apply {
		// Reverse order for safe deletion: children before parents.
		for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
			files[i], files[j] = files[j], files[i]
		}
	}

	// Build the REST mapper once for the entire directory walk.
	mapper, err := c.buildRESTMapper()
	if err != nil {
		return fmt.Errorf("failed to build REST mapper: %w", err)
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during manifest dir processing: %w", ctx.Err())
		default:
		}

		if err := c.processFile(ctx, f, mapper, apply); err != nil {
			return fmt.Errorf("failed to process %s: %w", f, err)
		}
	}

	return nil
}

// processFile decodes all YAML documents in path and applies or deletes each resource.
func (c *K8sClient) processFile(ctx context.Context, path string, mapper meta.RESTMapper, apply bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read manifest file %s: %w", path, err)
	}

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var raw runtime.RawExtension
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode YAML in %s: %w", path, err)
		}
		if len(raw.Raw) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{}
		_, gvk, err := decUnstructured.Decode(raw.Raw, nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode object in %s: %w", path, err)
		}

		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("failed to get REST mapping for %s: %w", gvk.Kind, err)
		}

		dr := c.resourceInterface(mapping, obj.GetNamespace())

		if apply {
			_, err = dr.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{
				FieldManager: "kgateway-bench-runner",
				Force:        true,
			})
		} else {
			err = dr.Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
			if err != nil && strings.Contains(err.Error(), "not found") {
				err = nil
			}
		}

		if err != nil {
			return fmt.Errorf("failed to %s %s/%s: %w", opName(apply), gvk.Kind, obj.GetName(), err)
		}
	}

	return nil
}

// buildRESTMapper constructs a deferred discovery REST mapper backed by an in-memory cache.
// Creating this once per directory walk (rather than once per file) avoids redundant
// discovery API calls to the Kubernetes API server.
func (c *K8sClient) buildRESTMapper() (meta.RESTMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(c.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc)), nil
}

// resourceInterface returns a namespaced or cluster-scoped dynamic resource interface.
func (c *K8sClient) resourceInterface(mapping *meta.RESTMapping, namespace string) dynamic.ResourceInterface {
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return c.Dynamic.Resource(mapping.Resource).Namespace(namespace)
	}
	return c.Dynamic.Resource(mapping.Resource)
}

// yamlFilesInDir returns sorted absolute paths of all *.yaml / *.yml files in dir.
func yamlFilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			files = append(files, filepath.Join(dir, name))
		}
	}

	sort.Strings(files)
	return files, nil
}

// opName returns a human-readable label for error messages.
func opName(apply bool) string {
	if apply {
		return "apply"
	}
	return "delete"
}
