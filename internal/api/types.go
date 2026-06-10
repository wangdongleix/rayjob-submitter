package api

type submitRayJobRequest struct {
	Repository       string            `json:"repository"`
	CommitSHA        string            `json:"commit_sha"`
	Branch           string            `json:"branch"`
	GitHubRunID      string            `json:"github_run_id"`
	GitHubRunAttempt string            `json:"github_run_attempt"`
	Image            string            `json:"image"`
	NameSuffix       string            `json:"name_suffix"`
	Script           string            `json:"script"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env"`
	WorkerReplicas   int               `json:"worker_replicas"`
}

type submitRayJobResponse struct {
	JobID      string `json:"job_id"`
	RayJobName string `json:"rayjob_name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	Message    string `json:"message"`
}

type rayJobStatusResponse struct {
	JobID               string `json:"job_id"`
	RayJobName          string `json:"rayjob_name"`
	Namespace           string `json:"namespace"`
	Status              string `json:"status"`
	RayJobStatus        string `json:"rayjob_status"`
	JobDeploymentStatus string `json:"job_deployment_status"`
}

type watchEvent struct {
	Type                string `json:"type"`
	Status              string `json:"status,omitempty"`
	RayJobStatus        string `json:"rayjob_status,omitempty"`
	JobDeploymentStatus string `json:"job_deployment_status,omitempty"`
	Message             string `json:"message,omitempty"`
	Pod                 string `json:"pod,omitempty"`
	Container           string `json:"container,omitempty"`
	Line                string `json:"line,omitempty"`
	Timestamp           string `json:"timestamp,omitempty"`
}

type cleanupWorkflowResponse struct {
	GitHubRunID      string   `json:"github_run_id"`
	GitHubRunAttempt string   `json:"github_run_attempt"`
	DeletedCount     int      `json:"deleted_count"`
	DeletedJobIDs    []string `json:"deleted_job_ids"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
