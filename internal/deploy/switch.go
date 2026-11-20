package deploy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

const (
	// healthTimeout is how long a candidate has to pass its gate. It is an
	// Executor field, so tests can shorten it.
	healthTimeout = 60 * time.Second
	// healthInterval is the pause between two probes.
	healthInterval = time.Second
	// probeTimeout bounds one probe, so a hanging answer cannot hold the gate.
	probeTimeout = 2 * time.Second
	// healthPasses is how many probes in a row must pass.
	healthPasses = 3
	// portlessRun is how long a candidate with no port must keep running.
	portlessRun = 10 * time.Second
)

// healthClient is the one HTTP client the gate probes with. It dials the
// container directly whatever HTTP_PROXY says, never keeps a connection, and
// never follows a redirect: the status is the answer.
var healthClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// SwitchRelease runs the health gate on the ready candidate, pushes the complete
// proxy config with the candidate routed, and commits the switch in one state
// transaction (docs/specs/release.md, "SwitchRelease").
func (x *Executor) SwitchRelease(ctx context.Context, s *agentv1.SwitchRelease) *agentv1.CommandResult {
	appID := s.GetAppId()
	releaseID := s.GetReleaseId()
	defer x.lockApp(appID)()

	b, st, known, err := x.db.App(ctx, appID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, fmt.Sprintf("read app %s: %v", appID, err), release.State{})
	}
	if !known {
		return result(agentv1.CommandOutcome_REJECTED,
			fmt.Sprintf("app %s has no release on this server; deploy it first", appID), st)
	}
	next, err := st.Switch(releaseID)
	if err != nil {
		return result(agentv1.CommandOutcome_REJECTED, err.Error(), st)
	}

	rec, found, err := x.db.Release(ctx, appID, releaseID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, err.Error(), st)
	}
	if !found {
		return result(agentv1.CommandOutcome_FAILED,
			fmt.Sprintf("release %s of app %s has no record on this server; deploy it again", releaseID, appID), st)
	}

	if gateErr := x.healthGate(ctx, b.Project, rec); gateErr != nil {
		return x.abortSwitch(ctx, st, rec, gateErr.Error())
	}

	routed := state.CurrentRelease{Binding: b, Record: rec}
	if err := x.proxy.PushRouted(ctx, routed, func(ctx context.Context) error {
		return x.db.RecordSwitch(ctx, appID, releaseID, next, x.now())
	}); err != nil {
		return result(agentv1.CommandOutcome_FAILED, err.Error(), st)
	}
	return result(agentv1.CommandOutcome_SUCCEEDED, "", next)
}

// abortSwitch clears the candidate, removes its container, records the aborted
// row and answers FAILED. Current and Previous never change, so the old release
// keeps serving.
func (x *Executor) abortSwitch(ctx context.Context, st release.State, rec state.Record, cause string) *agentv1.CommandResult {
	aborted, err := st.Abort(rec.ReleaseID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, cause+"; aborting the release also failed: "+err.Error(), st)
	}
	detail := truncate(cause)
	if rmErr := x.engine.remove(ctx, rec.ContainerID); rmErr != nil && !isNotFound(rmErr) {
		detail += "; removing the candidate container also failed: " + rmErr.Error()
	}
	if recErr := x.db.RecordAbort(ctx, rec.AppID, rec.ReleaseID, aborted, truncate(detail)); recErr != nil {
		detail += "; recording the abort also failed: " + recErr.Error()
		return result(agentv1.CommandOutcome_FAILED, detail, st)
	}
	return result(agentv1.CommandOutcome_FAILED, detail, aborted)
}

// healthGate blocks until the candidate is healthy, it exits or restarts, or the
// limit passes. It starts no goroutine. The probe target is the container's own
// address on its project network and the release's stored port and health path,
// so nothing in a message can aim it at another host (docs/threat-model.md §3
// B3). It never reads a probe's body into the answer (§9.3).
func (x *Executor) healthGate(ctx context.Context, project string, rec state.Record) error {
	gateCtx, cancel := context.WithTimeout(ctx, x.healthLimit)
	defer cancel()

	start := time.Now()
	base, err := x.engine.runState(gateCtx, rec.ContainerID)
	if err != nil {
		if gateCtx.Err() != nil {
			return x.gateTimeout(rec, "reading its container timed out")
		}
		return fmt.Errorf("release %s of app %s: container %s: %w", rec.ReleaseID, rec.AppID, rec.ContainerID, err)
	}
	if !base.Running {
		return notRunning(rec, base)
	}

	addr := ""
	if rec.Port != 0 {
		ip, ipErr := x.engine.containerIP(gateCtx, rec.ContainerID, "mg-"+project)
		if ipErr != nil {
			if gateCtx.Err() != nil {
				return x.gateTimeout(rec, "reading its address timed out")
			}
			return fmt.Errorf("release %s of app %s: container %s: %w", rec.ReleaseID, rec.AppID, rec.ContainerID, ipErr)
		}
		addr = net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(rec.Port), 10))
	}

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()
	passes := 0
	last := "no probe passed"
	for {
		cur, runErr := x.engine.runState(gateCtx, rec.ContainerID)
		if runErr != nil {
			if gateCtx.Err() != nil {
				return x.gateTimeout(rec, last)
			}
			return fmt.Errorf("release %s of app %s: container %s: %w", rec.ReleaseID, rec.AppID, rec.ContainerID, runErr)
		}
		if !cur.Running {
			return notRunning(rec, cur)
		}
		if cur.RestartCount != base.RestartCount {
			return fmt.Errorf("container %s restarted during the health gate (restart count %d, was %d); the candidate is not stable, so the old release keeps serving",
				rec.ContainerID, cur.RestartCount, base.RestartCount)
		}

		if addr == "" {
			if time.Since(start) >= portlessRun {
				return nil
			}
		} else {
			probeErr := probe(gateCtx, addr, rec.HealthPath)
			if probeErr == nil {
				passes++
				if passes >= healthPasses {
					return nil
				}
			} else {
				if gateCtx.Err() != nil {
					return x.gateTimeout(rec, last)
				}
				passes = 0
				last = probeErr.Error()
			}
		}

		select {
		case <-gateCtx.Done():
			return x.gateTimeout(rec, last)
		case <-ticker.C:
		}
	}
}

// gateTimeout is the error of a gate that ran out of time. It names the last
// probe failure, never its body.
func (x *Executor) gateTimeout(rec state.Record, last string) error {
	return fmt.Errorf("release %s of app %s did not pass its health gate within %s: %s; the old release keeps serving",
		rec.ReleaseID, rec.AppID, x.healthLimit, last)
}

// notRunning is the gate's error for a container that stopped.
func notRunning(rec state.Record, s runState) error {
	return fmt.Errorf("container %s exited (status %s, exit code %d); the candidate is not running, so the old release keeps serving",
		rec.ContainerID, s.Status, s.ExitCode)
}

// probe is one health probe of the candidate. With no path it dials the address;
// with a path it GETs it. It drains the answer with a bounded read and never
// turns the body into a value: an app can print its own environment.
func probe(ctx context.Context, addr, path string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	if path == "" {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial %s: %w", addr, err)
		}
		return conn.Close()
	}

	url := "http://" + addr + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build GET %s: %w", url, err)
	}
	res, err := healthClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10)); err != nil {
		return fmt.Errorf("GET %s: read its answer: %w", url, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 399 {
		return fmt.Errorf("GET %s answered status %d", url, res.StatusCode)
	}
	return nil
}
