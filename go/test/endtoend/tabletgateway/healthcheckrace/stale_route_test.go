/*
Copyright 2026 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package healthcheckrace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"vitess.io/vitess/go/mysql"
	"vitess.io/vitess/go/mysql/sqlerror"
	"vitess.io/vitess/go/test/endtoend/utils"
	"vitess.io/vitess/go/vt/topo"

	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
)

const (
	// raceBudget bounds the dice-rolling loop. The fatal interleaving is
	// scheduler-dependent, so a single run may well not reproduce it; the
	// budget caps how long one run keeps trying.
	raceBudget = 5 * time.Minute

	// recoverTimeout is how long the replica may stay unroutable after a
	// ReplaceTablet before the routing state is declared poisoned. Benign
	// unroutable windows during a replace are millisecond-scale; the
	// poisoned state is permanent, so a generous timeout only rules out
	// resource-starved environments, it cannot mask a reproduction.
	recoverTimeout = 15 * time.Second

	// replicaLoadConns is the number of connections hammering the replica
	// target throughout the test. Their queries contend with the racing
	// health updates on the healthcheck mutex and keep vtgate's scheduler
	// busy, both of which production has and an idle test cluster lacks.
	replicaLoadConns = 8

	// noHealthyTablet is the routing answer vtgate gives when the healthy
	// list is empty for a target (tabletgateway.go). Seeing it sustained,
	// while the healthcheck cache shows the replica serving, is the bug's
	// externally observable signature.
	noHealthyTablet = "no healthy tablet available"
)

// TestReplicaRoutableAfterReplaceTablet asserts that a replica which is
// serving and streaming good health stays routable across ReplaceTablet
// cycles. It is a probabilistic reproducer for the race in which the
// canceled checkConn goroutine of the replaced tablet delivers its final
// updateHealth(Serving: false) after the replacement connection has already
// reported healthy, permanently dropping the tablet from the healthy
// (routing) list while the healthcheck cache keeps showing it serving.
//
// Each iteration flips a non-grpc entry of the replica's port map in its
// topo record. That changes the tablet's address key — what the topology
// watcher sees when a tablet comes back under the same alias with a changed
// address, e.g. a rescheduled pod — so the watcher calls ReplaceTablet while
// the old healthcheck stream is still live. A live stream at replace time is
// the only trigger shape where the canceled goroutine is guaranteed to be
// inside stream() and issue the racing final update: a restart with downtime
// usually parks the old goroutine in its retry backoff sleep, where
// cancellation exits cleanly.
//
// The fatal ordering is left entirely to the schedulers of the real vtgate
// and vttablet processes, so any single iteration usually stays benign.
// When the race does hit, the failure carries the full observable state:
// vtgate's sustained "no healthy tablet" routing answer for the replica, a
// working primary as cross-check, the healthcheck cache still showing the
// replica serving, and the vtgate healthcheck log fingerprint.
func TestReplicaRoutableAfterReplaceTablet(t *testing.T) {
	ctx := t.Context()

	require.NoError(t, clusterInstance.VtgateProcess.WaitForStatusOfTabletInShard(keyspaceName+".0.primary", 1, 30*time.Second))
	require.NoError(t, clusterInstance.VtgateProcess.WaitForStatusOfTabletInShard(keyspaceName+".0.replica", 1, 30*time.Second))

	conn, err := mysql.Connect(ctx, &vtParams)
	require.NoError(t, err)
	defer conn.Close()
	utils.Exec(t, conn, "insert into t1(id, val) values (1, 1)")

	ts, err := topo.OpenServer(*clusterInstance.TopoFlavorString(), clusterInstance.VtctldClientProcess.TopoGlobalAddress, clusterInstance.VtctldClientProcess.TopoGlobalRoot)
	require.NoError(t, err)
	defer ts.Close()

	replica := clusterInstance.Keyspaces[0].Shards[0].Replica()
	replicaAlias := replica.GetAlias()
	baseVtPort := replica.HTTPPort

	replicaProbe := &targetProbe{target: keyspaceName + "@replica"}
	defer replicaProbe.close()
	primaryProbe := &targetProbe{target: keyspaceName + "@primary"}
	defer primaryProbe.close()

	loadCtx, stopLoad := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range replicaLoadConns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runReplicaLoad(loadCtx)
		}()
	}
	defer func() {
		stopLoad()
		wg.Wait()
	}()

	deadline := time.Now().Add(raceBudget)
	iteration := 0
	for time.Now().Before(deadline) {
		iteration++
		prevReplaces := replaceTabletOps(t)

		// Same alias, same grpc endpoint, different address key: flipping
		// the record's non-grpc port changes TabletToMapKey without
		// touching the live healthcheck stream.
		_, err := ts.UpdateTabletFields(ctx, replicaAlias, func(tablet *topodatapb.Tablet) error {
			tablet.PortMap["vt"] = int32(baseVtPort + iteration%2)
			return nil
		})
		require.NoError(t, err)

		// Wait for the topology watcher to act on the changed record. This
		// is the moment the race runs inside vtgate.
		require.Eventually(t, func() bool {
			return replaceTabletOps(t) > prevReplaces
		}, 30*time.Second, 50*time.Millisecond, "topology watcher never ran ReplaceTablet for the changed tablet record")

		report := waitReplicaRoutable(ctx, replicaProbe, recoverTimeout)
		if report == nil {
			if iteration%25 == 0 {
				t.Logf("iteration %d: clean (ReplaceTablet ops so far: %d)", iteration, replaceTabletOps(t))
			}
			continue
		}

		// The replica never became routable again. The bug's signature is
		// the split-brain: the healthcheck cache (which is refreshed on
		// every update, trivial ones included) shows the replica serving on
		// a live stream, while the healthy list (rebuilt only on
		// non-trivial updates) has permanently lost it.
		cacheServing := clusterInstance.VtgateProcess.GetStatusForTabletOfShard(keyspaceName+".0.replica", 1)
		primaryErr := primaryProbe.query(ctx)

		t.Logf("iteration %d: @replica queries failed continuously for %v (%d failures)", iteration, recoverTimeout, report.failures)
		t.Logf("first error: %v", report.firstErr)
		t.Logf("last error: %v", report.lastErr)
		t.Logf("vtgate routing answer for the replica target: %v", report.routingErr)
		t.Logf("@primary queries still work: %v (error if not: %v)", primaryErr == nil, primaryErr)
		t.Logf("healthcheck cache still counts the replica as serving: %v", cacheServing)
		t.Logf("vtgate /debug/gateway (healthcheck cache view):\n%s", vtgateDebugGateway(t))
		t.Logf("vtgate healthcheck log fingerprint (last lines):\n%s", vtgateHealthcheckLogLines(t))

		require.True(t, cacheServing,
			"replica is unroutable and also missing from the healthcheck cache: the cluster is genuinely unhealthy, which is not the stale-update race")
		require.NoErrorf(t, primaryErr,
			"primary is unroutable too: vtgate itself is unhealthy, which is not the stale-update race")
		require.NotNilf(t, report.routingErr,
			"replica stayed unroutable for %v but vtgate never answered %q: inconclusive, the probes may not have observed routing at all (first: %v, last: %v)",
			recoverTimeout, noHealthyTablet, report.firstErr, report.lastErr)
		require.Failf(t, "stale health update race reproduced",
			"replica %s is serving and streaming health (per the healthcheck cache) but stayed out of the healthy list for %v after ReplaceTablet; vtgate answers: %v",
			replicaAlias, recoverTimeout, report.routingErr)
	}

	t.Logf("no reproduction in %d ReplaceTablet cycles over %v", iteration, raceBudget)
}

// targetProbe is a persistent connection to one vtgate target. Keeping the
// connection across probes means a probe result is vtgate's routing answer;
// fresh connections per probe can fail at the socket level under connection
// churn and observe nothing.
type targetProbe struct {
	target string
	conn   *mysql.Conn
}

// query runs a single read against the probe's target, reconnecting first if
// the previous connection died.
func (p *targetProbe) query(ctx context.Context) error {
	if p.conn == nil || p.conn.IsClosed() {
		conn, err := mysql.Connect(ctx, &vtParams)
		if err != nil {
			return err
		}
		if _, err := conn.ExecuteFetch("use `"+p.target+"`", 1, false); err != nil {
			conn.Close()
			return err
		}
		p.conn = conn
	}
	_, err := p.conn.ExecuteFetch("select id from t1", 1, false)
	if err != nil && sqlerror.IsConnErr(err) {
		p.conn.Close()
		p.conn = nil
	}
	return err
}

func (p *targetProbe) close() {
	if p.conn != nil {
		p.conn.Close()
	}
}

// unroutableReport describes a window in which the replica target never
// became stably routable.
type unroutableReport struct {
	failures int
	firstErr error
	lastErr  error
	// routingErr is the first error in the window that was vtgate's
	// empty-healthy-list routing answer, if any.
	routingErr error
}

// waitReplicaRoutable returns nil once an @replica query has succeeded twice
// in a row, 250ms apart. A single success is not enough: it could land in
// the sub-millisecond gap between the replacement connection's first healthy
// update and the stale update poisoning the healthy list. If the replica is
// still unroutable when the timeout expires, the collected errors are
// returned.
func waitReplicaRoutable(ctx context.Context, probe *targetProbe, timeout time.Duration) *unroutableReport {
	deadline := time.Now().Add(timeout)
	report := &unroutableReport{}
	successes := 0
	for time.Now().Before(deadline) {
		if err := probe.query(ctx); err != nil {
			report.failures++
			report.lastErr = err
			if report.firstErr == nil {
				report.firstErr = err
			}
			if report.routingErr == nil && strings.Contains(err.Error(), noHealthyTablet) {
				report.routingErr = err
			}
			successes = 0
			time.Sleep(100 * time.Millisecond)
			continue
		}
		successes++
		if successes == 2 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if report.firstErr == nil {
		report.firstErr = fmt.Errorf("replica did not become stably routable within %v", timeout)
		report.lastErr = report.firstErr
	}
	return report
}

// runReplicaLoad keeps one connection's worth of query load on the replica
// target until ctx is canceled. Errors only pace the loop and, when the
// connection died, trigger a reconnect: the load exists to contend with the
// racing health updates, not to assert anything. The pacing matters: an
// unpaced error loop reconnects thousands of times per second and exhausts
// client sockets, which would blind the probes.
func runReplicaLoad(ctx context.Context) {
	var conn *mysql.Conn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	for ctx.Err() == nil {
		if conn == nil || conn.IsClosed() {
			c, err := mysql.Connect(ctx, &vtParams)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if _, err := c.ExecuteFetch("use `"+keyspaceName+"@replica`", 1, false); err != nil {
				c.Close()
				time.Sleep(100 * time.Millisecond)
				continue
			}
			conn = c
		}
		if _, err := conn.ExecuteFetch("select id from t1", 1, false); err != nil {
			if sqlerror.IsConnErr(err) {
				conn.Close()
				conn = nil
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// replaceTabletOps reads the topology watcher's ReplaceTablet operation
// counter from vtgate's /debug/vars.
func replaceTabletOps(t *testing.T) int {
	vars := clusterInstance.VtgateProcess.GetVars()
	require.NotNil(t, vars, "could not fetch vtgate /debug/vars")
	ops, ok := vars["TopologyWatcherOperations"].(map[string]any)
	if !ok {
		return 0
	}
	count, _ := ops["ReplaceTablet"].(float64)
	return int(count)
}

// vtgateDebugGateway fetches vtgate's /debug/gateway page: the healthcheck
// cache rendered as JSON.
func vtgateDebugGateway(t *testing.T) string {
	url := fmt.Sprintf("http://%s:%d/debug/gateway", clusterInstance.Hostname, clusterInstance.VtgateProcess.Port)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// vtgateHealthcheckLogLines returns the tail of vtgate's healthcheck-related
// log lines: tablets being added/removed/replaced and serving-state
// transitions. In a reproduction these show the replacement connection's
// "serving false => true" and the canceled connection's stale "serving true
// => false" racing each other, with no later recovery.
func vtgateHealthcheckLogLines(t *testing.T) string {
	var lines []string
	sources := []string{clusterInstance.VtgateProcess.ErrorLog}
	if matches, err := filepath.Glob(filepath.Join(clusterInstance.TmpDirectory, "vtgate*INFO*")); err == nil {
		sources = append(sources, matches...)
	}
	for _, source := range sources {
		data, err := os.ReadFile(source)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "healthcheck") || strings.Contains(line, "HealthCheckUpdate") {
				lines = append(lines, line)
			}
		}
	}
	const keep = 60
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return strings.Join(lines, "\n")
}
