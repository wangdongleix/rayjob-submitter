package api

import (
	"context"
	"log"
	"time"

	"github.com/verl-project/rayjob-submitter/internal/kubernetes"
)

func (s *Server) RunStaleRayJobCleanup(ctx context.Context) {
	if !s.cfg.StaleCleanupEnabled {
		log.Printf("stale_rayjob_cleanup enabled=false")
		return
	}

	ttl := time.Duration(s.cfg.StaleCleanupTTL) * time.Second
	interval := time.Duration(s.cfg.StaleCleanupInterval) * time.Second
	log.Printf(
		"stale_rayjob_cleanup enabled=true namespace=%s ttl_seconds=%d interval_seconds=%d",
		s.cfg.TargetNamespace,
		s.cfg.StaleCleanupTTL,
		s.cfg.StaleCleanupInterval,
	)

	s.cleanupStaleRayJobs(ctx, ttl)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("stale_rayjob_cleanup stopped")
			return
		case <-ticker.C:
			s.cleanupStaleRayJobs(ctx, ttl)
		}
	}
}

func (s *Server) cleanupStaleRayJobs(ctx context.Context, ttl time.Duration) {
	result, err := kubernetes.DeleteStaleRayJobs(ctx, s.dynamicClient, s.cfg.TargetNamespace, time.Now().UTC(), ttl)
	if err != nil {
		log.Printf(
			"stale_rayjob_cleanup error=%q scanned=%d deleted=%d skipped_fresh=%d skipped_no_creation_time=%d deleted_job_ids=%q",
			err,
			result.Scanned,
			result.Deleted,
			result.SkippedFresh,
			result.SkippedNoCreationTime,
			result.DeletedJobIDs,
		)
		return
	}
	log.Printf(
		"stale_rayjob_cleanup scanned=%d deleted=%d skipped_fresh=%d skipped_no_creation_time=%d deleted_job_ids=%q skipped_fresh_job_ids=%q skipped_no_creation_job_ids=%q",
		result.Scanned,
		result.Deleted,
		result.SkippedFresh,
		result.SkippedNoCreationTime,
		result.DeletedJobIDs,
		result.SkippedFreshJobIDs,
		result.SkippedNoCreationJobIDs,
	)
}
