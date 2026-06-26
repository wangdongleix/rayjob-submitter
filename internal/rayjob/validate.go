package rayjob

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/verl-project/rayjob-submitter/internal/config"
)

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	numberPattern     = regexp.MustCompile(`^[0-9]+$`)
	branchPattern     = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	nameSuffixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}[a-z0-9]$|^[a-z0-9]$`)
	envKeyPattern     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	invalidNameChars  = regexp.MustCompile(`[^a-z0-9-]+`)
	repeatedDash      = regexp.MustCompile(`-+`)
)

const MaxRayJobNameLength = 47

var protectedEnv = map[string]struct{}{
	"PATH":                  {},
	"HOME":                  {},
	"PYTHONPATH":            {},
	"LD_LIBRARY_PATH":       {},
	"RAY_ADDRESS":           {},
	"RAY_HEAD_SERVICE_HOST": {},
}

type SubmitRequest struct {
	Repository       string
	CommitSHA        string
	Branch           string
	GitHubRunID      string
	GitHubRunAttempt string
	Image            string
	NameSuffix       string
	Script           string
	Args             []string
	Env              map[string]string
	WorkerReplicas   int
	TemplatePatches  []TemplatePatch
}

func ValidateSubmitRequest(req SubmitRequest, cfg config.Config) error {
	if req.Repository != cfg.AllowedRepository {
		return fmt.Errorf("repository is not allowed")
	}
	if !shaPattern.MatchString(req.CommitSHA) {
		return fmt.Errorf("commit_sha must be a 40-character lowercase hex SHA")
	}
	if req.Branch == "" || !branchPattern.MatchString(req.Branch) {
		return fmt.Errorf("branch contains invalid characters")
	}
	if !numberPattern.MatchString(req.GitHubRunID) {
		return fmt.Errorf("github_run_id must contain digits only")
	}
	if !numberPattern.MatchString(req.GitHubRunAttempt) {
		return fmt.Errorf("github_run_attempt must contain digits only")
	}
	if req.Image == "" {
		return fmt.Errorf("image is required")
	}
	if !hasAnyPrefix(req.Image, cfg.AllowedImagePrefixes) {
		return fmt.Errorf("image is not allowed")
	}
	if err := validateNameSuffix(req.NameSuffix); err != nil {
		return err
	}
	if err := validateScript(req.Script, cfg); err != nil {
		return err
	}
	if err := validateArgs(req.Args, cfg); err != nil {
		return err
	}
	if err := validateEnv(req.Env, cfg); err != nil {
		return err
	}
	if req.WorkerReplicas <= 0 || req.WorkerReplicas > cfg.MaxWorkerReplicas {
		return fmt.Errorf("worker_replicas is not allowed")
	}
	if err := ValidateTemplatePatches(req.TemplatePatches, cfg); err != nil {
		return err
	}
	return nil
}

func ValidateWorkflowIDs(runID, attempt string) error {
	if !numberPattern.MatchString(runID) {
		return fmt.Errorf("github_run_id must contain digits only")
	}
	if !numberPattern.MatchString(attempt) {
		return fmt.Errorf("github_run_attempt must contain digits only")
	}
	return nil
}

func validateNameSuffix(nameSuffix string) error {
	if nameSuffix == "" {
		return fmt.Errorf("name_suffix is required")
	}
	if !nameSuffixPattern.MatchString(nameSuffix) {
		return fmt.Errorf("name_suffix is invalid")
	}
	return nil
}

func validateScript(script string, cfg config.Config) error {
	if script == "" {
		return fmt.Errorf("script is required")
	}
	if strings.Contains(script, "..") || path.Clean(script) != script {
		return fmt.Errorf("script must be a clean path")
	}
	if strings.ContainsAny(script, " ;|&`$<>\\\"'*?[]{}()!\t\n\r") {
		return fmt.Errorf("script contains unsafe characters")
	}
	if !hasAnyPrefix(script, cfg.AllowedScriptPrefixes) {
		return fmt.Errorf("script prefix is not allowed")
	}
	if !strings.HasSuffix(script, ".sh") && !strings.HasSuffix(script, ".py") {
		return fmt.Errorf("script must be a .sh or .py file")
	}
	return nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func validateArgs(args []string, cfg config.Config) error {
	if len(args) > cfg.MaxArgs {
		return fmt.Errorf("too many args")
	}
	for _, arg := range args {
		if len(arg) > cfg.MaxArgLength {
			return fmt.Errorf("arg is too long")
		}
		if strings.ContainsAny(arg, "\x00\n\r;|&`<>") {
			return fmt.Errorf("arg contains unsafe characters")
		}
	}
	return nil
}

func validateEnv(env map[string]string, cfg config.Config) error {
	if len(env) > cfg.MaxExtraEnv {
		return fmt.Errorf("too many env vars")
	}
	for key, value := range env {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("env key is invalid")
		}
		if _, found := protectedEnv[key]; found {
			return fmt.Errorf("env key is protected")
		}
		if len(value) > cfg.MaxEnvValueLength || strings.Contains(value, "\x00") {
			return fmt.Errorf("env value is invalid")
		}
	}
	return nil
}

func JobID(runID, attempt string, timestampMillis int64, nameSuffix string) string {
	runID = dnsPart(runID, "0")
	attempt = dnsPart(attempt, "1")
	suffix := dnsPart(nameSuffix, "job")
	shortTS := time.UnixMilli(timestampMillis).UTC().Format("150405")

	prefix := fmt.Sprintf("vrj-%s-%s-%s", runID, attempt, shortTS)
	maxSuffix := MaxRayJobNameLength - len(prefix) - 1
	if maxSuffix < 1 {
		runID = truncatePart(runID, 12)
		attempt = truncatePart(attempt, 3)
		prefix = fmt.Sprintf("vrj-%s-%s-%s", runID, attempt, shortTS)
		maxSuffix = MaxRayJobNameLength - len(prefix) - 1
	}
	name := prefix + "-" + truncatePart(suffix, maxSuffix)
	if len(name) > MaxRayJobNameLength {
		name = strings.TrimRight(name[:MaxRayJobNameLength], "-")
	}
	return dnsPart(name, "vrj-job")
}

func dnsPart(s, fallback string) string {
	s = strings.ToLower(s)
	s = invalidNameChars.ReplaceAllString(s, "-")
	s = repeatedDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return fallback
	}
	return s
}

func truncatePart(s string, max int) string {
	if max < 1 {
		return ""
	}
	if len(s) > max {
		s = s[:max]
	}
	return strings.Trim(s, "-")
}
