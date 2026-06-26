package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr                  string
	TargetNamespace             string
	WorkerReplicas              int
	MaxWorkerReplicas           int
	AllowedRepository           string
	AllowedImagePrefixes        []string
	RayJobTemplatePath          string
	LogHeadWaitSeconds          int
	WatchTimeoutSeconds         int
	StatusPollSeconds           int
	StaleCleanupEnabled         bool
	StaleCleanupTTL             int
	StaleCleanupInterval        int
	AllowedScriptPrefixes       []string
	MaxArgs                     int
	MaxArgLength                int
	MaxExtraEnv                 int
	MaxEnvValueLength           int
	MaxTemplatePatches          int
	MaxTemplatePatchValueLength int
}

const (
	defaultListenAddr                  = ":8080"
	defaultTargetNamespace             = "ray"
	defaultWorkerReplicas              = 2
	defaultMaxWorkerReplicas           = 2
	defaultAllowedRepository           = "verl-project/verl"
	defaultAllowedImagePrefixes        = "quay.io/ascend/verl:,quay.io/albert54/verl-ci:"
	defaultRayJobTemplatePath          = "templates/verl-rayjob.yaml"
	defaultLogHeadWaitSeconds          = 1800
	defaultWatchTimeoutSeconds         = 12600
	defaultStatusPollSeconds           = 30
	defaultStaleCleanupEnabled         = true
	defaultStaleCleanupTTL             = 21600
	defaultStaleCleanupInterval        = 600
	defaultAllowedScriptPrefixes       = "tests/special_npu/,tests/special_e2e/,examples/data_preprocess/,/root/.cache/cicd_test/"
	defaultMaxArgs                     = 64
	defaultMaxArgLength                = 1024
	defaultMaxExtraEnv                 = 64
	defaultMaxEnvValueLength           = 2048
	defaultMaxTemplatePatches          = 32
	defaultMaxTemplatePatchValueLength = 2048
)

func Load() (Config, error) {
	workerReplicas, err := intEnv("WORKER_REPLICAS", defaultWorkerReplicas)
	if err != nil {
		return Config{}, err
	}
	maxWorkerReplicas, err := intEnv("MAX_WORKER_REPLICAS", defaultMaxWorkerReplicas)
	if err != nil {
		return Config{}, err
	}
	logHeadWaitSeconds, err := intEnv("LOG_HEAD_WAIT_SECONDS", defaultLogHeadWaitSeconds)
	if err != nil {
		return Config{}, err
	}
	watchTimeoutSeconds, err := intEnv("WATCH_TIMEOUT_SECONDS", defaultWatchTimeoutSeconds)
	if err != nil {
		return Config{}, err
	}
	statusPollSeconds, err := intEnv("STATUS_POLL_SECONDS", defaultStatusPollSeconds)
	if err != nil {
		return Config{}, err
	}
	staleCleanupEnabled, err := boolEnv("STALE_RAYJOB_CLEANUP_ENABLED", defaultStaleCleanupEnabled)
	if err != nil {
		return Config{}, err
	}
	staleCleanupTTL, err := intEnv("STALE_RAYJOB_TTL_SECONDS", defaultStaleCleanupTTL)
	if err != nil {
		return Config{}, err
	}
	staleCleanupInterval, err := intEnv("STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS", defaultStaleCleanupInterval)
	if err != nil {
		return Config{}, err
	}
	maxArgs, err := intEnv("MAX_ARGS", defaultMaxArgs)
	if err != nil {
		return Config{}, err
	}
	maxArgLength, err := intEnv("MAX_ARG_LENGTH", defaultMaxArgLength)
	if err != nil {
		return Config{}, err
	}
	maxExtraEnv, err := intEnv("MAX_EXTRA_ENV", defaultMaxExtraEnv)
	if err != nil {
		return Config{}, err
	}
	maxEnvValueLength, err := intEnv("MAX_ENV_VALUE_LENGTH", defaultMaxEnvValueLength)
	if err != nil {
		return Config{}, err
	}
	maxTemplatePatches, err := intEnv("MAX_TEMPLATE_PATCHES", defaultMaxTemplatePatches)
	if err != nil {
		return Config{}, err
	}
	maxTemplatePatchValueLength, err := intEnv("MAX_TEMPLATE_PATCH_VALUE_LENGTH", defaultMaxTemplatePatchValueLength)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ListenAddr:                  stringEnv("LISTEN_ADDR", defaultListenAddr),
		TargetNamespace:             stringEnv("TARGET_NAMESPACE", defaultTargetNamespace),
		WorkerReplicas:              workerReplicas,
		MaxWorkerReplicas:           maxWorkerReplicas,
		AllowedRepository:           stringEnv("ALLOWED_REPOSITORY", defaultAllowedRepository),
		AllowedImagePrefixes:        listEnv("ALLOWED_IMAGE_PREFIXES", defaultAllowedImagePrefixes),
		RayJobTemplatePath:          stringEnv("RAYJOB_TEMPLATE_PATH", defaultRayJobTemplatePath),
		LogHeadWaitSeconds:          logHeadWaitSeconds,
		WatchTimeoutSeconds:         watchTimeoutSeconds,
		StatusPollSeconds:           statusPollSeconds,
		StaleCleanupEnabled:         staleCleanupEnabled,
		StaleCleanupTTL:             staleCleanupTTL,
		StaleCleanupInterval:        staleCleanupInterval,
		AllowedScriptPrefixes:       listEnv("ALLOWED_SCRIPT_PREFIXES", defaultAllowedScriptPrefixes),
		MaxArgs:                     maxArgs,
		MaxArgLength:                maxArgLength,
		MaxExtraEnv:                 maxExtraEnv,
		MaxEnvValueLength:           maxEnvValueLength,
		MaxTemplatePatches:          maxTemplatePatches,
		MaxTemplatePatchValueLength: maxTemplatePatchValueLength,
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.TargetNamespace == "" {
		return fmt.Errorf("TARGET_NAMESPACE is required")
	}
	if c.AllowedRepository == "" {
		return fmt.Errorf("ALLOWED_REPOSITORY is required")
	}
	if len(c.AllowedImagePrefixes) == 0 {
		return fmt.Errorf("ALLOWED_IMAGE_PREFIXES is required")
	}
	if c.RayJobTemplatePath == "" {
		return fmt.Errorf("RAYJOB_TEMPLATE_PATH is required")
	}
	if c.WorkerReplicas <= 0 {
		return fmt.Errorf("WORKER_REPLICAS must be positive")
	}
	if c.MaxWorkerReplicas < c.WorkerReplicas {
		return fmt.Errorf("MAX_WORKER_REPLICAS must be >= WORKER_REPLICAS")
	}
	if c.LogHeadWaitSeconds <= 0 {
		return fmt.Errorf("LOG_HEAD_WAIT_SECONDS must be positive")
	}
	if c.WatchTimeoutSeconds <= 0 {
		return fmt.Errorf("WATCH_TIMEOUT_SECONDS must be positive")
	}
	if c.StatusPollSeconds <= 0 {
		return fmt.Errorf("STATUS_POLL_SECONDS must be positive")
	}
	if c.StaleCleanupTTL <= 0 {
		return fmt.Errorf("STALE_RAYJOB_TTL_SECONDS must be positive")
	}
	if c.StaleCleanupInterval <= 0 {
		return fmt.Errorf("STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS must be positive")
	}
	if len(c.AllowedScriptPrefixes) == 0 {
		return fmt.Errorf("ALLOWED_SCRIPT_PREFIXES is required")
	}
	if c.MaxArgs < 0 || c.MaxArgLength <= 0 || c.MaxExtraEnv < 0 || c.MaxEnvValueLength <= 0 {
		return fmt.Errorf("argument and env limits must be valid")
	}
	if c.MaxTemplatePatches < 0 || c.MaxTemplatePatchValueLength <= 0 {
		return fmt.Errorf("template patch limits must be valid")
	}
	return nil
}

func stringEnv(name, defaultValue string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return defaultValue
}

func intEnv(name string, defaultValue int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return parsed, nil
}

func boolEnv(name string, defaultValue bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return parsed, nil
}

func listEnv(name, defaultValue string) []string {
	parts := strings.Split(stringEnv(name, defaultValue), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
