package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/verl-project/rayjob-submitter/internal/kubernetes"
	"github.com/verl-project/rayjob-submitter/internal/rayjob"
)

const (
	logContainer     = "ray-job-submitter"
	logDrainTimeout  = 30 * time.Second
	logOverlap       = 2 * time.Second
	maxReplayRecords = 65536
	maxLogLine       = 4 * 1024 * 1024
)

// Only this coordinator emits final. The live reader and drain reader never run
// concurrently, so queued log events precede final and cursor ownership is exclusive.
func (s *Server) coordinateWatch(ctx context.Context, jobID string, events chan<- watchEvent) {
	terminal := make(chan watchEvent, 1)
	go s.watchRayJobStatus(ctx, jobID, events, func(ev watchEvent) {
		select {
		case terminal <- ev:
		case <-ctx.Done():
		}
	})
	reader := &recoveringLogs{server: s, jobID: jobID, events: events}
	liveCtx, stopLive := context.WithCancel(ctx)
	defer stopLive()
	liveDone := make(chan struct{})
	go func() { defer close(liveDone); reader.run(liveCtx, true) }()
	var result watchEvent
	select {
	case <-ctx.Done():
		stopLive()
		<-liveDone
		return
	case result = <-terminal:
	}
	stopLive()
	<-liveDone
	if ctx.Err() != nil {
		return
	}
	if result.Status == rayjob.StatusSucceeded || result.Status == rayjob.StatusFailed {
		drainCtx, cancel := context.WithTimeout(ctx, logDrainTimeout)
		err := reader.run(drainCtx, false)
		cancel()
		if err != nil && ctx.Err() == nil {
			sendEvent(ctx, events, watchEvent{Type: "error", Message: "final log drain incomplete: " + err.Error()})
		}
	} else {
		sendEvent(ctx, events, watchEvent{Type: "error", Message: "logs may be incomplete: final drain skipped for " + result.Status})
	}
	if ctx.Err() == nil {
		finishOnce(ctx, events)(result)
	}
}

type replayKey struct {
	timestamp time.Time
	digest    [32]byte
}

type logCursor struct {
	started   bool
	latest    time.Time
	cutoff    time.Time
	records   map[replayKey]int
	disabled  bool
	malformed bool
}

func (c *logCursor) options(follow bool) corev1.PodLogOptions {
	options := corev1.PodLogOptions{Container: logContainer, Follow: follow, Timestamps: true}
	if !c.started {
		tail := int64(1000)
		options.TailLines = &tail
	} else if !c.latest.IsZero() {
		// metav1.Time.MarshalQueryParameter uses RFC3339 (whole seconds).
		// Round down explicitly; retain every record in the actual requested window.
		since := metav1.NewTime(c.latest.Add(-logOverlap).Truncate(time.Second))
		options.SinceTime = &since
	}
	return options
}

func (c *logCursor) replay() map[replayKey]int {
	replay := make(map[replayKey]int, len(c.records))
	for key, count := range c.records {
		replay[key] = count
	}
	return replay
}

// Counts, not a set, suppress only occurrences delivered on earlier connections.
// Newly encountered records retain their source order, including identical lines.
func (c *logCursor) accept(raw string, replay map[replayKey]int, warn func(string), emit func(string) bool) bool {
	prefix, line, found := strings.Cut(raw, " ")
	timestamp, err := time.Parse(time.RFC3339Nano, prefix)
	if !found || err != nil {
		if !c.malformed {
			warn("log timestamp malformed; preserving raw lines; replay may duplicate these records")
			c.malformed = true
		}
		return emit(raw)
	}
	timestamp = timestamp.UTC()
	key := replayKey{timestamp: timestamp, digest: sha256.Sum256([]byte(line))}
	if replay[key] > 0 {
		replay[key]--
		return false
	}
	if !emit(line) {
		return false
	}
	if timestamp.After(c.latest) {
		c.latest = timestamp
		cutoff := timestamp.Add(-logOverlap).Truncate(time.Second)
		if cutoff.After(c.cutoff) {
			for old := range c.records {
				if old.timestamp.Before(cutoff) {
					delete(c.records, old)
				}
			}
			c.cutoff = cutoff
		}
	}
	if !c.disabled && !timestamp.Before(c.latest.Add(-logOverlap).Truncate(time.Second)) {
		if c.records == nil {
			c.records = make(map[replayKey]int)
		}
		if _, exists := c.records[key]; !exists && len(c.records) >= maxReplayRecords {
			c.disabled = true
			c.records = nil
			clear(replay)
			warn("log replay tracking limit exceeded; replay suppression disabled for this container; duplicates possible")
		} else {
			c.records[key]++
		}
	}
	return true
}

type recoveringLogs struct {
	server   *Server
	jobID    string
	events   chan<- watchEvent
	identity string
	cursor   logCursor
	fatal    error
}

func (r *recoveringLogs) diagnostic(ctx context.Context, message string) {
	sendEvent(ctx, r.events, watchEvent{Type: "error", Message: message})
}

func permanentLogError(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsMethodNotSupported(err)
}

func containerIdentity(pod *corev1.Pod) (string, bool) {
	identity := string(pod.UID) + "/" + logContainer
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == logContainer {
			return fmt.Sprintf("%s/%d/%s", identity, status.RestartCount, status.ContainerID), status.State.Terminated != nil
		}
	}
	return identity, pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// follow=false repeatedly snapshots until a read made after container termination
// finishes. A caller-owned deadline bounds containers still writing terminal output.
func (r *recoveringLogs) run(ctx context.Context, follow bool) error {
	if r.fatal != nil {
		return r.fatal
	}
	discoveryDeadline := time.Now().Add(time.Duration(r.server.cfg.LogHeadWaitSeconds) * time.Second)
	backoff := time.Second
	interrupted := false
	for ctx.Err() == nil {
		attemptCtx := ctx
		cancelAttempt := func() {}
		if follow && r.identity == "" {
			attemptCtx, cancelAttempt = context.WithDeadline(ctx, discoveryDeadline)
		}
		pod, err := kubernetes.FindRayJobSubmitterPod(attemptCtx, r.server.typedClient, r.server.cfg.TargetNamespace, r.jobID)
		cancelAttempt()
		progress := false
		if err == nil && pod != nil {
			identity, terminated := containerIdentity(pod)
			if identity != r.identity {
				if r.identity != "" {
					r.diagnostic(ctx, "submitter pod/container changed; resetting log cursor; previous-container logs are not recovered and a log gap is possible")
				}
				r.identity = identity
				r.cursor = logCursor{}
				sendEvent(ctx, r.events, watchEvent{Type: "status", Message: "Streaming logs from pod " + pod.Name + " container " + logContainer})
			}
			if interrupted {
				sendEvent(ctx, r.events, watchEvent{Type: "status", Message: "reconnecting submitter logs from saved cursor"})
			}
			options := r.cursor.options(follow && !terminated)
			stream, openErr := kubernetes.OpenPodLogStream(ctx, r.server.typedClient, r.server.cfg.TargetNamespace, pod.Name, options)
			err = openErr
			if err == nil {
				r.cursor.started = true
				// Closing on cancellation also handles an idle body read. Do not defer
				// closes in this loop: every attempt releases its connection immediately.
				stopClose := context.AfterFunc(ctx, func() { _ = stream.Close() })
				scanner := bufio.NewScanner(stream)
				scanner.Buffer(make([]byte, 64*1024), maxLogLine)
				replay := r.cursor.replay()
				for scanner.Scan() {
					if ctx.Err() != nil {
						break
					}
					accepted := r.cursor.accept(scanner.Text(), replay, func(message string) { r.diagnostic(ctx, message) }, func(line string) bool {
						select {
						case r.events <- watchEvent{Type: "log", Pod: pod.Name, Container: logContainer, Line: line}:
							return true
						case <-ctx.Done():
							return false
						}
					})
					if ctx.Err() != nil {
						break
					}
					if !accepted {
						continue
					}
					progress = true
					if interrupted {
						sendEvent(ctx, r.events, watchEvent{Type: "status", Message: "submitter log stream recovered"})
						interrupted = false
					}
				}
				err = scanner.Err()
				stopClose()
				_ = stream.Close()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, bufio.ErrTooLong) {
					r.fatal = fmt.Errorf("unrecoverable log gap: line exceeds %d bytes; log collection stopped", maxLogLine)
					r.diagnostic(ctx, r.fatal.Error())
					return r.fatal
				}
				if err == nil && terminated {
					return nil
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if permanentLogError(err) {
			r.fatal = fmt.Errorf("log collection unavailable: %w", err)
			r.diagnostic(ctx, r.fatal.Error())
			return r.fatal
		}
		if follow && r.identity == "" && !time.Now().Before(discoveryDeadline) {
			err = fmt.Errorf("timed out waiting for RayJob submitter pod; logs incomplete")
			r.diagnostic(ctx, err.Error())
			return err
		}
		if follow || err != nil {
			message := "log stream interrupted (EOF); retrying"
			if err != nil {
				message = "log collection interrupted; retrying: " + err.Error()
			}
			r.diagnostic(ctx, message)
			interrupted = true
		}
		if progress {
			backoff = time.Second
		}
		delay := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1))
		if !follow {
			delay = time.Second
		}
		if follow && r.identity == "" {
			delay = min(delay, time.Until(discoveryDeadline))
		}
		if !sleepOrDone(ctx, delay) {
			return ctx.Err()
		}
		backoff = min(30*time.Second, backoff*2)
	}
	return ctx.Err()
}
