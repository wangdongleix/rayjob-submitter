package api

import (
	"bufio"
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
		JobID:          jobID,
		Namespace:      s.cfg.TargetNamespace,
		Repository:     req.Repository,
		CommitSHA:      req.CommitSHA,
		Branch:         req.Branch,
		GitHubRunID:    req.GitHubRunID,
		GitHubAttempt:  req.GitHubRunAttempt,
		Image:          req.Image,
		NameSuffix:     req.NameSuffix,
		Script:         req.Script,
		Args:           req.Args,
		Env:            req.Env,
		WorkerReplicas: req.WorkerReplicas,
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
	finish := finishOnce(ctx, events)

	go s.watchRayJobStatus(ctx, jobID, events, finish)
	go s.streamRayJobLogEvents(ctx, jobID, events)

	flusher, _ := w.(http.Flusher)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			if ev.Timestamp == "" {
				ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
			}
			if format == "ndjson" {
				_ = json.NewEncoder(w).Encode(ev)
			} else {
				fmt.Fprintln(w, formatWatchText(ev))
			}
			if flusher != nil {
				flusher.Flush()
			}
			if ev.Type == "final" {
				return
			}
		}
	}
}

func (s *Server) watchRayJobStatus(ctx context.Context, jobID string, events chan<- watchEvent, finish func(watchEvent)) {
	deadline := time.Now().Add(time.Duration(s.cfg.WatchTimeoutSeconds) * time.Second)
	tick := time.Duration(s.cfg.StatusPollSeconds) * time.Second
	lastStatus := ""
	for {
		if time.Now().After(deadline) {
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
			return
		}
	}
}

func (s *Server) streamRayJobLogEvents(ctx context.Context, jobID string, events chan<- watchEvent) {
	deadline := time.Now().Add(time.Duration(s.cfg.LogHeadWaitSeconds) * time.Second)
	for {
		pod, err := kubernetes.FindRayJobSubmitterPod(ctx, s.typedClient, s.cfg.TargetNamespace, jobID)
		if err == nil && pod != nil {
			sendEvent(ctx, events, watchEvent{Type: "status", Status: rayjob.StatusRunning, Message: "streaming logs from pod " + pod.Name})
			stream, err := kubernetes.OpenPodLogStream(ctx, s.typedClient, s.cfg.TargetNamespace, pod.Name, "ray-job-submitter", 1000)
			if err != nil {
				sendEvent(ctx, events, watchEvent{Type: "error", Message: err.Error()})
				return
			}
			defer stream.Close()
			scanner := bufio.NewScanner(stream)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				sendEvent(ctx, events, watchEvent{Type: "log", Pod: pod.Name, Container: "ray-job-submitter", Line: scanner.Text()})
			}
			if err := scanner.Err(); err != nil && ctx.Err() == nil {
				sendEvent(ctx, events, watchEvent{Type: "error", Message: "log stream ended: " + err.Error()})
			}
			return
		}
		if time.Now().After(deadline) {
			sendEvent(ctx, events, watchEvent{Type: "error", Message: "timed out waiting for RayJob submitter pod"})
			return
		}
		sendEvent(ctx, events, watchEvent{Type: "status", Status: rayjob.StatusPending, Message: "waiting for RayJob submitter pod"})
		if !sleepOrDone(ctx, 10*time.Second) {
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
	flusher, _ := w.(http.Flusher)
	deadline := time.Now().Add(time.Duration(s.cfg.LogHeadWaitSeconds) * time.Second)
	for {
		pod, err := kubernetes.FindRayJobSubmitterPod(r.Context(), s.typedClient, s.cfg.TargetNamespace, jobID)
		if err == nil && pod != nil {
			fmt.Fprintf(w, "Streaming logs from pod %s container ray-job-submitter\n", pod.Name)
			if flusher != nil {
				flusher.Flush()
			}
			audit(r, "logs_stream_start", map[string]any{"job_id": jobID, "pod": pod.Name})
			if err := kubernetes.StreamPodLogs(r.Context(), s.typedClient, s.cfg.TargetNamespace, pod.Name, "ray-job-submitter", flushWriter{w: w, f: flusher}); err != nil {
				fmt.Fprintf(w, "\nfailed to stream logs from pod %s container ray-job-submitter: %v\n", pod.Name, err)
				if flusher != nil {
					flusher.Flush()
				}
				audit(r, "logs_stream_failed", map[string]any{"job_id": jobID, "pod": pod.Name})
			} else {
				audit(r, "logs_stream_done", map[string]any{"job_id": jobID, "pod": pod.Name})
			}
			return
		}
		if pod != nil && pod.Status.Phase == "Failed" {
			fmt.Fprintf(w, "RayJob submitter pod %s failed before logs could be streamed\n", pod.Name)
			if flusher != nil {
				flusher.Flush()
			}
			audit(r, "logs_head_failed", map[string]any{"job_id": jobID, "pod": pod.Name})
			return
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(w, "timed out waiting for RayJob submitter pod for job %s\n", jobID)
			if flusher != nil {
				flusher.Flush()
			}
			audit(r, "logs_wait_timeout", map[string]any{"job_id": jobID})
			return
		}
		fmt.Fprintf(w, "waiting for RayJob submitter pod for job %s\n", jobID)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
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

type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

func auditFields(req submitRayJobRequest, jobID string) map[string]any {
	return map[string]any{
		"job_id":          jobID,
		"repository":      req.Repository,
		"commit_sha":      req.CommitSHA,
		"branch":          req.Branch,
		"github_run_id":   req.GitHubRunID,
		"image":           req.Image,
		"name_suffix":     req.NameSuffix,
		"script":          req.Script,
		"worker_replicas": req.WorkerReplicas,
	}
}
