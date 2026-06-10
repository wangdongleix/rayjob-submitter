package rayjob

import "testing"

func TestNormalizeStatus(t *testing.T) {
	tests := []struct {
		jobStatus        string
		deploymentStatus string
		want             string
	}{
		{jobStatus: "SUCCEEDED", want: "SUCCEEDED"},
		{jobStatus: "FAILED", want: "FAILED"},
		{jobStatus: "RUNNING", want: "RUNNING"},
		{deploymentStatus: "ValidationFailed", want: "FAILED"},
		{want: "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := NormalizeStatus(tt.jobStatus, tt.deploymentStatus); got != tt.want {
			t.Fatalf("NormalizeStatus(%q, %q) = %q, want %q", tt.jobStatus, tt.deploymentStatus, got, tt.want)
		}
	}
}
