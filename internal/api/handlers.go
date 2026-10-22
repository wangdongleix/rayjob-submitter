package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/verl-project/rayjob-submitter/internal/kubernetes"
	"github.com/verl-project/rayjob-submitter/internal/rayjob"
)

var jobIDPattern = regexp.MustCompile(`^vrj-[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) submitRayJob(w http.ResponseWriter, r *http.Request) {
	var req submitRayJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON", Message: err.Error()})
		audit(r, "bad_request", nil)
		return
	}
	if err := rayjob.ValidateSubmitRequest(rayjob.SubmitRequest(req), s.cfg); err != nil {
		status := http.StatusForbidden
		writeJSON(w, status, errorResponse{Error: "validation failed", Message: err.Error()})
		audit(r, "validation_failed", auditFields(req, ""))
		return
	}

	jobID := rayjob.JobID(req.GitHubRunID, req.GitHubRunAttempt, time.Now().UTC().UnixMilli(), req.NameSuffix)
	obj, err := rayjob.PatchRayJobTemplate(s.rayJobTemplate, rayjob.RenderParams{
		JobID:           jobID,
		Namespace:       s.cfg.TargetNamespace,
		Repository:      req.Repository,
		CommitSHA:       req.CommitSHA,
		Branch:          req.Branch,
		GitHubRunID:     req.GitHubRunID,
		GitHubAttempt:   req.GitHubRunAttempt,
		Image:           req.Image,
		NameSuffix:      req.NameSuffix,
		Script:          req.Script,
		Args:            req.Args,
		Env:             req.Env,
		WorkerReplicas:  req.WorkerReplicas,
		TemplatePatches: req.TemplatePatches,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to render RayJob", Message: err.Error()})
		audit(r, "render_failed", auditFields(req, jobID))
		return
	}

	err = kubernetes.CreateRayJob(r.Context(), s.dynamicClient, s.cfg.TargetNamespace, obj)
	if apierrors.IsAlreadyExists(err) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "RayJob already exists"})
		audit(r, "already_exists", auditFields(req, jobID))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create RayJob", Message: err.Error()})
		audit(r, "create_failed", auditFields(req, jobID))
		return
	}
	writeJSON(w, http.StatusOK, submitRayJobResponse{
		JobID:      jobID,
		RayJobName: jobID,
		Namespace:  s.cfg.TargetNamespace,
		Status:     rayjob.StatusSubmitted,
		Message:    "RayJob submitted",
	})
	audit(r, "submitted", auditFields(req, jobID))
}

func (s *Server) getRayJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	if !isValidJobID(jobID) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid job_id"})
		audit(r, "invalid_job_id", map[string]any{"job_id": jobID})
		return
	}
	obj, err := kubernetes.GetRayJob(r.Context(), s.dynamicClient, s.cfg.TargetNamespace, jobID)
	if apierrors.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, rayJobStatusResponse{
			JobID:      jobID,
			RayJobName: jobID,
			Namespace:  s.cfg.TargetNamespace,
			Status:     rayjob.StatusNotFound,
		})
		audit(r, "not_found", map[string]any{"job_id": jobID})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to get RayJob", Message: err.Error()})
		audit(r, "get_failed", map[string]any{"job_id": jobID})
		return
	}
	status, jobStatus, deploymentStatus := rayJobStatus(obj)
	writeJSON(w, http.StatusOK, rayJobStatusResponse{
		JobID:               jobID,
		RayJobName:          jobID,
		Namespace:           s.cfg.TargetNamespace,
		Status:              status,
		RayJobStatus:        jobStatus,
		JobDeploymentStatus: deploymentStatus,
	})
	audit(r, "status", map[string]any{"job_id": jobID, "status": status})
}

func (s *Server) cleanupWorkflow(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("github_run_id")
	attempt := r.URL.Query().Get("attempt")
	if err := rayjob.ValidateWorkflowIDs(runID, attempt); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid workflow id", Message: err.Error()})
		audit(r, "cleanup_invalid_workflow", map[string]any{"github_run_id": runID, "github_run_attempt": attempt})
		return
	}
	deleted, err := kubernetes.DeleteRayJobsByWorkflow(r.Context(), s.dynamicClient, s.cfg.TargetNamespace, runID, attempt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to cleanup workflow", Message: err.Error()})
		audit(r, "cleanup_failed", map[string]any{"github_run_id": runID, "github_run_attempt": attempt})
		return
	}
	writeJSON(w, http.StatusOK, cleanupWorkflowResponse{
		GitHubRunID:      runID,
		GitHubRunAttempt: attempt,
		DeletedCount:     len(deleted),
		DeletedJobIDs:    deleted,
	})
	audit(r, "cleanup_done", map[string]any{"github_run_id": runID, "github_run_attempt": attempt, "deleted_count": len(deleted)})
}

func (s *Server) watchRayJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	if !isValidJobID(jobID) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid job_id"})
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "text" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid format"})
		return
	}
	if format == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan watchEvent, 128)
	go s.coordinateWatch(ctx, jobID, events)

	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			// Bound a blocked client write as well as upstream reads.
			if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil && err != http.ErrNotSupported {
				return
			}
			if ev.Timestamp == "" {
				ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
			}
			var err error
			if format == "ndjson" {
				err = json.NewEncoder(w).Encode(ev)
			} else {
				_, err = fmt.Fprintln(w, formatWatchText(ev))
			}
			if err != nil {
				return
			}
			if err := controller.Flush(); err != nil && err != http.ErrNotSupported {
				return
			}
			if err := controller.SetWriteDeadline(time.Time{}); err != nil && err != http.ErrNotSupported {
				return
			}
			if ev.Type == "final" {
				return
			}
		}
	}
}

func (s *Server) watchRayJobStatus(ctx context.Context, jobID string, events chan<- watchEvent, finish func(watchEvent)) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.WatchTimeoutSeconds)*time.Second)
	defer cancel()
	tick := time.Duration(s.cfg.StatusPollSeconds) * time.Second
	lastStatus := ""
	for {
		if ctx.Err() != nil {
			finish(watchEvent{Status: "TIMEOUT", Message: "Timed out waiting for RayJob"})
			return
		}
		obj, err := kubernetes.GetRayJob(ctx, s.dynamicClient, s.cfg.TargetNamespace, jobID)
		if apierrors.IsNotFound(err) {
			finish(watchEvent{Status: rayjob.StatusNotFound, Message: "RayJob not found"})
			return
		}
		if err != nil {
			sendEvent(ctx, events, watchEvent{Type: "error", Message: err.Error()})
			if !sleepOrDone(ctx, tick) {
				finish(watchEvent{Status: "TIMEOUT", Message: "Timed out waiting for RayJob"})
				return
			}
			continue
		}

		status, rayStatus, deployStatus := rayJobStatus(obj)
		if status != lastStatus {
			sendEvent(ctx, events, watchEvent{
				Type:                "status",
				Status:              status,
				RayJobStatus:        rayStatus,
				JobDeploymentStatus: deployStatus,
				Message:             "RayJob is " + strings.ToLower(status),
			})
			lastStatus = status
		}
		if status == rayjob.StatusSucceeded || status == rayjob.StatusFailed {
			finish(watchEvent{
				Status:              status,
				RayJobStatus:        rayStatus,
				JobDeploymentStatus: deployStatus,
				Message:             "RayJob " + strings.ToLower(status),
			})
			return
		}
		if !sleepOrDone(ctx, tick) {
			finish(watchEvent{Status: "TIMEOUT", Message: "Timed out waiting for RayJob"})
			return
		}
	}
}

func (s *Server) streamRayJobLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	if !isValidJobID(jobID) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid job_id"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan watchEvent, 128)
	go s.coordinateWatch(ctx, jobID, events)
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			// Bound a blocked client write as well as upstream reads.
			if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil && err != http.ErrNotSupported {
				return
			}
			if ev.Type == "final" {
				return
			}
			line := ev.Message
			if ev.Type == "log" {
				line = ev.Line
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return
			}
			if err := controller.Flush(); err != nil && err != http.ErrNotSupported {
				return
			}
			if err := controller.SetWriteDeadline(time.Time{}); err != nil && err != http.ErrNotSupported {
				return
			}
		}
	}
}

func rayJobStatus(obj *unstructured.Unstructured) (string, string, string) {
	rayStatus, _, _ := unstructured.NestedString(obj.Object, "status", "jobStatus")
	deployStatus, _, _ := unstructured.NestedString(obj.Object, "status", "jobDeploymentStatus")
	return rayjob.NormalizeStatus(rayStatus, deployStatus), rayStatus, deployStatus
}

func sendEvent(ctx context.Context, events chan<- watchEvent, ev watchEvent) {
	select {
	case <-ctx.Done():
	case events <- ev:
	}
}

func finishOnce(ctx context.Context, events chan<- watchEvent) func(watchEvent) {
	var once sync.Once
	return func(ev watchEvent) {
		once.Do(func() {
			ev.Type = "final"
			sendEvent(ctx, events, ev)
		})
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func formatWatchText(ev watchEvent) string {
	switch ev.Type {
	case "log":
		return "::log::" + ev.Line
	case "error":
		return "::error::" + ev.Message
	case "final":
		return "::final::" + ev.Status + "::" + ev.Message
	default:
		return "::status::" + ev.Status + "::" + ev.Message
	}
}

func isValidJobID(jobID string) bool {
	return len(jobID) <= rayjob.MaxRayJobNameLength && jobIDPattern.MatchString(jobID)
}

func auditFields(req submitRayJobRequest, jobID string) map[string]any {
	return map[string]any{
		"job_id":           jobID,
		"repository":       req.Repository,
		"commit_sha":       req.CommitSHA,
		"branch":           req.Branch,
		"github_run_id":    req.GitHubRunID,
		"image":            req.Image,
		"name_suffix":      req.NameSuffix,
		"script":           req.Script,
		"worker_replicas":  req.WorkerReplicas,
		"template_patches": len(req.TemplatePatches),
	}
}
