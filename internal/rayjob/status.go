package rayjob

import "strings"

const (
	StatusSubmitted = "SUBMITTED"
	StatusPending   = "PENDING"
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusUnknown   = "UNKNOWN"
	StatusNotFound  = "NOT_FOUND"
)

func NormalizeStatus(jobStatus, deploymentStatus string) string {
	if strings.EqualFold(deploymentStatus, "ValidationFailed") {
		return StatusFailed
	}
	switch strings.ToUpper(jobStatus) {
	case StatusSucceeded:
		return StatusSucceeded
	case StatusFailed:
		return StatusFailed
	case StatusRunning:
		return StatusRunning
	case StatusPending:
		return StatusPending
	default:
		return StatusUnknown
	}
}
