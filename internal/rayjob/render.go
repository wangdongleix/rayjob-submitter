package rayjob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type RenderParams struct {
	JobID          string
	Namespace      string
	Repository     string
	CommitSHA      string
	Branch         string
	GitHubRunID    string
	GitHubAttempt  string
	Image          string
	NameSuffix     string
	Script         string
	Args           []string
	Env            map[string]string
	WorkerReplicas int
}

func LoadRayJobTemplate(path string) (*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(obj); err != nil {
		return nil, err
	}
	if obj.GetKind() != "RayJob" {
		return nil, fmt.Errorf("template kind must be RayJob")
	}
	return obj, nil
}

func PatchRayJobTemplate(template *unstructured.Unstructured, p RenderParams) (*unstructured.Unstructured, error) {
	obj := template.DeepCopy()
	obj.SetName(p.JobID)
	obj.SetNamespace(p.Namespace)
	obj.SetLabels(addStrings(obj.GetLabels(), commonLabels(p)))
	obj.SetAnnotations(addStrings(obj.GetAnnotations(), map[string]string{
		"ci.github.com/repository":  p.Repository,
		"ci.github.com/commit-sha":  p.CommitSHA,
		"ci.github.com/branch":      p.Branch,
		"ci.github.com/run-id":      p.GitHubRunID,
		"ci.github.com/run-attempt": p.GitHubAttempt,
		"ci.github.com/image":       p.Image,
		"ci.verl/script":            p.Script,
		"ci.verl/args-sha256":       argsSHA256(p.Args),
	}))
	if err := unstructured.SetNestedField(obj.Object, BuildEntrypoint(p.Script, p.Args), "spec", "entrypoint"); err != nil {
		return nil, err
	}
	if err := patchSubmitterPod(obj, p); err != nil {
		return nil, err
	}
	if err := patchPodTemplate(obj, p, "spec", "rayClusterSpec", "headGroupSpec", "template"); err != nil {
		return nil, err
	}
	if err := patchWorkerGroups(obj, p); err != nil {
		return nil, err
	}
	return obj, nil
}

func patchSubmitterPod(obj *unstructured.Unstructured, p RenderParams) error {
	containers, ok, err := unstructured.NestedSlice(obj.Object, "spec", "submitterPodTemplate", "spec", "containers")
	if err != nil || !ok {
		return fmt.Errorf("template missing submitterPodTemplate containers")
	}
	if !patchContainerImage(containers, "ray-job-submitter", p.Image) {
		return fmt.Errorf("template missing ray-job-submitter container")
	}
	if err := appendExtraEnv(containers, p.Env); err != nil {
		return err
	}
	return unstructured.SetNestedSlice(obj.Object, containers, "spec", "submitterPodTemplate", "spec", "containers")
}

func patchPodTemplate(obj *unstructured.Unstructured, p RenderParams, fields ...string) error {
	labels, ok, err := unstructured.NestedStringMap(obj.Object, append(fields, "metadata", "labels")...)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("template missing pod labels")
	}
	if err := unstructured.SetNestedStringMap(obj.Object, addStrings(labels, commonLabels(p)), append(fields, "metadata", "labels")...); err != nil {
		return err
	}
	containersPath := append(fields, "spec", "containers")
	containers, ok, err := unstructured.NestedSlice(obj.Object, containersPath...)
	if err != nil || !ok {
		return fmt.Errorf("template missing pod containers")
	}
	if !patchContainerImage(containers, "ray-head", p.Image) && !patchContainerImage(containers, "ray-worker", p.Image) {
		return fmt.Errorf("template missing ray container")
	}
	if err := appendExtraEnv(containers, p.Env); err != nil {
		return err
	}
	return unstructured.SetNestedSlice(obj.Object, containers, containersPath...)
}

func patchWorkerGroups(obj *unstructured.Unstructured, p RenderParams) error {
	groups, ok, err := unstructured.NestedSlice(obj.Object, "spec", "rayClusterSpec", "workerGroupSpecs")
	if err != nil || !ok {
		return fmt.Errorf("template missing workerGroupSpecs")
	}
	for i := range groups {
		group, ok := groups[i].(map[string]any)
		if !ok {
			return fmt.Errorf("invalid worker group")
		}
		group["replicas"] = int64(p.WorkerReplicas)
		group["minReplicas"] = int64(p.WorkerReplicas)
		group["maxReplicas"] = int64(p.WorkerReplicas)
		if err := patchPodTemplate(&unstructured.Unstructured{Object: group}, p, "template"); err != nil {
			return err
		}
	}
	return unstructured.SetNestedSlice(obj.Object, groups, "spec", "rayClusterSpec", "workerGroupSpecs")
}

func patchContainerImage(containers []any, name, image string) bool {
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if ok && container["name"] == name {
			container["image"] = image
			return true
		}
	}
	return false
}

func appendExtraEnv(containers []any, extra map[string]string) error {
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid container")
		}
		env := []any{}
		if raw, found := container["env"]; found {
			var ok bool
			env, ok = raw.([]any)
			if !ok {
				return fmt.Errorf("invalid env")
			}
		}
		existing := map[string]struct{}{}
		for _, envItem := range env {
			envMap, ok := envItem.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid env")
			}
			name, ok := envMap["name"].(string)
			if !ok || name == "" {
				return fmt.Errorf("invalid env name")
			}
			existing[name] = struct{}{}
		}
		for key, value := range extra {
			if _, found := existing[key]; found {
				return fmt.Errorf("env %s already exists in template", key)
			}
			env = append(env, map[string]any{"name": key, "value": value})
		}
		container["env"] = env
	}
	return nil
}

func commonLabels(p RenderParams) map[string]string {
	return map[string]string{
		"ci.verl/job-id":            p.JobID,
		"ci.verl/name-suffix":       p.NameSuffix,
		"ci.verl/run-id":            p.GitHubRunID,
		"ci.github.com/run-id":      p.GitHubRunID,
		"ci.github.com/run-attempt": p.GitHubAttempt,
	}
}

func addStrings(base, extra map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func argsSHA256(args []string) string {
	data, _ := json.Marshal(args)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
