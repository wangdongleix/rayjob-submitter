package rayjob

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/verl-project/rayjob-submitter/internal/config"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type TemplatePatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

type templatePatchKind string

const (
	patchKindResource      templatePatchKind = "resource"
	patchKindShmSizeLimit  templatePatchKind = "shm"
	patchKindRayStartParam templatePatchKind = "rayStartParam"
	patchKindEnvAdd        templatePatchKind = "env"
)

var rayStartParamPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidateTemplatePatches(patches []TemplatePatch, cfg config.Config) error {
	if len(patches) > cfg.MaxTemplatePatches {
		return fmt.Errorf("too many template patches")
	}
	for i, patch := range patches {
		kind, _, err := classifyTemplatePatch(patch)
		if err != nil {
			return fmt.Errorf("template_patches[%d]: %w", i, err)
		}
		if err := validateTemplatePatchValue(kind, patch, cfg); err != nil {
			return fmt.Errorf("template_patches[%d]: %w", i, err)
		}
	}
	return nil
}

func ApplyTemplatePatches(obj *unstructured.Unstructured, patches []TemplatePatch) error {
	for i, patch := range patches {
		kind, fields, err := classifyTemplatePatch(patch)
		if err != nil {
			return fmt.Errorf("template_patches[%d]: %w", i, err)
		}
		switch kind {
		case patchKindEnvAdd:
			env, err := templatePatchEnvValue(patch.Value)
			if err != nil {
				return fmt.Errorf("template_patches[%d]: %w", i, err)
			}
			if err := appendJSONPointer(obj.Object, fields[:len(fields)-1], map[string]any{"name": env.name, "value": env.value}); err != nil {
				return fmt.Errorf("template_patches[%d]: %w", i, err)
			}
		default:
			value, ok := patch.Value.(string)
			if !ok {
				return fmt.Errorf("template patch value must be a string")
			}
			if err := setJSONPointer(obj.Object, fields, value, patch.Op); err != nil {
				return fmt.Errorf("template_patches[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func classifyTemplatePatch(patch TemplatePatch) (templatePatchKind, []string, error) {
	if patch.Op != "add" && patch.Op != "replace" {
		return "", nil, fmt.Errorf("op must be add or replace")
	}
	fields, err := decodeJSONPointer(patch.Path)
	if err != nil {
		return "", nil, err
	}
	if isResourcePath(fields) {
		return patchKindResource, fields, nil
	}
	if isShmSizeLimitPath(fields) {
		return patchKindShmSizeLimit, fields, nil
	}
	if isRayStartParamPath(fields) {
		if !rayStartParamPattern.MatchString(fields[len(fields)-1]) {
			return "", nil, fmt.Errorf("rayStartParams key is invalid")
		}
		return patchKindRayStartParam, fields, nil
	}
	if isEnvAddPath(fields) {
		if patch.Op != "add" {
			return "", nil, fmt.Errorf("env patch op must be add")
		}
		return patchKindEnvAdd, fields, nil
	}
	return "", nil, fmt.Errorf("path is not allowed")
}

func validateTemplatePatchValue(kind templatePatchKind, patch TemplatePatch, cfg config.Config) error {
	data, err := json.Marshal(patch.Value)
	if err != nil {
		return fmt.Errorf("value must be JSON serializable")
	}
	if len(data) > cfg.MaxTemplatePatchValueLength {
		return fmt.Errorf("template patch value is too long")
	}
	switch kind {
	case patchKindResource, patchKindShmSizeLimit:
		value, ok := patch.Value.(string)
		if !ok || value == "" {
			return fmt.Errorf("template patch value must be a non-empty string")
		}
		if _, err := resource.ParseQuantity(value); err != nil {
			return fmt.Errorf("template patch value must be a Kubernetes quantity")
		}
	case patchKindRayStartParam:
		value, ok := patch.Value.(string)
		if !ok || value == "" || strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("rayStartParams value is invalid")
		}
	case patchKindEnvAdd:
		env, err := templatePatchEnvValue(patch.Value)
		if err != nil {
			return err
		}
		if err := validateEnv(map[string]string{env.name: env.value}, cfg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("template patch path is not allowed")
	}
	return nil
}

type patchEnvValue struct {
	name  string
	value string
}

func templatePatchEnvValue(value any) (patchEnvValue, error) {
	item, ok := value.(map[string]any)
	if !ok {
		return patchEnvValue{}, fmt.Errorf("env patch value must be an object")
	}
	name, ok := item["name"].(string)
	if !ok || name == "" {
		return patchEnvValue{}, fmt.Errorf("env patch value must include name")
	}
	envValue, ok := item["value"].(string)
	if !ok {
		return patchEnvValue{}, fmt.Errorf("env patch value must include string value")
	}
	if len(item) != 2 {
		return patchEnvValue{}, fmt.Errorf("env patch value may only include name and value")
	}
	return patchEnvValue{name: name, value: envValue}, nil
}

func decodeJSONPointer(path string) ([]string, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("path must be a JSON pointer")
	}
	raw := strings.Split(path[1:], "/")
	fields := make([]string, 0, len(raw))
	for _, item := range raw {
		decoded := strings.Builder{}
		for i := 0; i < len(item); i++ {
			if item[i] != '~' {
				decoded.WriteByte(item[i])
				continue
			}
			if i+1 >= len(item) {
				return nil, fmt.Errorf("path contains invalid escape")
			}
			switch item[i+1] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, fmt.Errorf("path contains invalid escape")
			}
			i++
		}
		fields = append(fields, decoded.String())
	}
	return fields, nil
}

func isResourcePath(fields []string) bool {
	return matchPrefixSuffix(fields, []string{"spec", "submitterPodTemplate", "spec", "containers", "0", "resources"}, []string{"requests", "limits"}, 8) ||
		matchPrefixSuffix(fields, []string{"spec", "rayClusterSpec", "headGroupSpec", "template", "spec", "containers", "0", "resources"}, []string{"requests", "limits"}, 10) ||
		matchPrefixSuffix(fields, []string{"spec", "rayClusterSpec", "workerGroupSpecs", "0", "template", "spec", "containers", "0", "resources"}, []string{"requests", "limits"}, 11)
}

func matchPrefixSuffix(fields, prefix, allowedKinds []string, wantLen int) bool {
	if len(fields) != wantLen || !hasPrefixFields(fields, prefix) {
		return false
	}
	kind := fields[len(fields)-2]
	resourceName := fields[len(fields)-1]
	return resourceName != "" && containsString(allowedKinds, kind)
}

func isShmSizeLimitPath(fields []string) bool {
	return equalFields(fields, []string{"spec", "rayClusterSpec", "headGroupSpec", "template", "spec", "volumes", "1", "emptyDir", "sizeLimit"}) ||
		equalFields(fields, []string{"spec", "rayClusterSpec", "workerGroupSpecs", "0", "template", "spec", "volumes", "1", "emptyDir", "sizeLimit"})
}

func isRayStartParamPath(fields []string) bool {
	return len(fields) == 5 && hasPrefixFields(fields, []string{"spec", "rayClusterSpec", "headGroupSpec", "rayStartParams"}) && fields[4] != "" ||
		len(fields) == 6 && hasPrefixFields(fields, []string{"spec", "rayClusterSpec", "workerGroupSpecs", "0", "rayStartParams"}) && fields[5] != ""
}

func isEnvAddPath(fields []string) bool {
	return equalFields(fields, []string{"spec", "rayClusterSpec", "headGroupSpec", "template", "spec", "containers", "0", "env", "-"}) ||
		equalFields(fields, []string{"spec", "rayClusterSpec", "workerGroupSpecs", "0", "template", "spec", "containers", "0", "env", "-"})
}

func hasPrefixFields(fields, prefix []string) bool {
	if len(fields) < len(prefix) {
		return false
	}
	for i := range prefix {
		if fields[i] != prefix[i] {
			return false
		}
	}
	return true
}

func equalFields(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func setJSONPointer(root any, fields []string, value any, op string) error {
	parent, key, err := jsonPointerParent(root, fields)
	if err != nil {
		return err
	}
	switch typed := parent.(type) {
	case map[string]any:
		if op == "replace" {
			if _, ok := typed[key]; !ok {
				return fmt.Errorf("replace path does not exist")
			}
		}
		typed[key] = value
		return nil
	case []any:
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 || index >= len(typed) {
			return fmt.Errorf("array index is invalid")
		}
		typed[index] = value
		return nil
	default:
		return fmt.Errorf("path parent is invalid")
	}
}

func appendJSONPointer(root any, fields []string, value any) error {
	parent, key, err := jsonPointerParent(root, fields)
	if err != nil {
		return err
	}
	switch typed := parent.(type) {
	case map[string]any:
		raw, ok := typed[key]
		if !ok {
			return fmt.Errorf("env path does not exist")
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("env path is not an array")
		}
		typed[key] = append(items, value)
		return nil
	default:
		return fmt.Errorf("env path parent is invalid")
	}
}

func jsonPointerParent(root any, fields []string) (any, string, error) {
	if len(fields) == 0 {
		return nil, "", fmt.Errorf("path is empty")
	}
	current := root
	for _, field := range fields[:len(fields)-1] {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[field]
			if !ok {
				return nil, "", fmt.Errorf("path does not exist")
			}
			current = next
		case []any:
			index, err := strconv.Atoi(field)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, "", fmt.Errorf("array index is invalid")
			}
			current = typed[index]
		default:
			return nil, "", fmt.Errorf("path does not exist")
		}
	}
	return current, fields[len(fields)-1], nil
}
