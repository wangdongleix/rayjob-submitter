package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadStaleCleanupDefaults(t *testing.T) {
	unsetEnv(t,
		"STALE_RAYJOB_CLEANUP_ENABLED",
		"STALE_RAYJOB_TTL_SECONDS",
		"STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS",
		"MAX_TEMPLATE_PATCHES",
		"MAX_TEMPLATE_PATCH_VALUE_LENGTH",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.StaleCleanupEnabled || cfg.StaleCleanupTTL != 21600 || cfg.StaleCleanupInterval != 600 {
		t.Fatalf("stale cleanup defaults = enabled:%t ttl:%d interval:%d", cfg.StaleCleanupEnabled, cfg.StaleCleanupTTL, cfg.StaleCleanupInterval)
	}
	if cfg.MaxTemplatePatches != 32 || cfg.MaxTemplatePatchValueLength != 2048 {
		t.Fatalf("template patch defaults = patches:%d value_length:%d", cfg.MaxTemplatePatches, cfg.MaxTemplatePatchValueLength)
	}
}

func TestLoadStaleCleanupDisabled(t *testing.T) {
	unsetEnv(t,
		"STALE_RAYJOB_TTL_SECONDS",
		"STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS",
	)
	t.Setenv("STALE_RAYJOB_CLEANUP_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.StaleCleanupEnabled {
		t.Fatalf("stale cleanup should be disabled")
	}
}

func TestValidateRejectsInvalidStaleCleanupDurations(t *testing.T) {
	cfg := Config{
		TargetNamespace:       "ray",
		WorkerReplicas:        2,
		MaxWorkerReplicas:     2,
		AllowedRepository:     "verl-project/verl",
		AllowedImagePrefixes:  []string{"quay.io/ascend/verl:"},
		RayJobTemplatePath:    "templates/verl-rayjob.yaml",
		LogHeadWaitSeconds:    1800,
		WatchTimeoutSeconds:   12600,
		StatusPollSeconds:     30,
		StaleCleanupEnabled:   true,
		StaleCleanupTTL:       21600,
		StaleCleanupInterval:  600,
		AllowedScriptPrefixes: []string{"tests/special_npu/"},
		MaxArgLength:          1024,
		MaxEnvValueLength:     2048,
	}

	cfg.StaleCleanupTTL = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "STALE_RAYJOB_TTL_SECONDS") {
		t.Fatalf("ttl validation error = %v", err)
	}

	cfg.StaleCleanupTTL = 21600
	cfg.StaleCleanupInterval = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS") {
		t.Fatalf("interval validation error = %v", err)
	}

	cfg.StaleCleanupInterval = 600
	cfg.MaxTemplatePatchValueLength = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "template patch limits") {
		t.Fatalf("template patch validation error = %v", err)
	}
}

func unsetEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, ok := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}
