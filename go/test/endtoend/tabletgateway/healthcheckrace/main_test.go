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

// Package healthcheckrace holds a probabilistic, flaky-by-design reproducer
// for the race between ReplaceTablet and the replaced tablet's canceled
// checkConn goroutine, which can leave a serving, health-streaming replica
// permanently absent from vtgate's healthy (routing) list.
//
// It is intentionally not wired into CI: each run is a bounded series of
// dice rolls against real binaries, not a deterministic regression test. The
// deterministic regression test for the same bug lives in
// go/vt/discovery/healthcheck_test.go.
package healthcheckrace

import (
	"flag"
	"os"
	"testing"

	"vitess.io/vitess/go/mysql"
	"vitess.io/vitess/go/test/endtoend/cluster"
)

var (
	clusterInstance *cluster.LocalProcessCluster
	vtParams        mysql.ConnParams
	keyspaceName    = "ks"
	cell            = "zone1"
	sqlSchema       = `create table t1(
		id bigint not null,
		val bigint,
		primary key(id)
	) ENGINE=InnoDB;`

	vSchema = `{
		"tables": {
			"t1": {}
		}
	}`
)

func TestMain(m *testing.M) {
	flag.Parse()

	exitCode := func() int {
		clusterInstance = cluster.NewCluster(cell, "localhost")
		defer clusterInstance.Teardown()

		// Frequent health stream updates so the healthcheck cache settles
		// quickly after every ReplaceTablet cycle.
		clusterInstance.VtTabletExtraArgs = []string{"--health-check-interval", "1s"}

		// A short topology refresh so each tablet record change turns into a
		// ReplaceTablet within milliseconds instead of the 1m default.
		// tablet-refresh-known-tablets (the default) is what makes the
		// watcher re-read and compare known tablet records at all.
		clusterInstance.VtGateExtraArgs = []string{
			"--tablet-refresh-interval", "250ms",
			"--tablet-refresh-known-tablets=true",
		}

		// Start topo server
		err := clusterInstance.StartTopo()
		if err != nil {
			return 1
		}

		// Start keyspace: one primary and one replica, so that losing the
		// replica from the healthy list is directly observable as @replica
		// queries failing.
		keyspace := &cluster.Keyspace{
			Name:      keyspaceName,
			SchemaSQL: sqlSchema,
			VSchema:   vSchema,
		}
		err = clusterInstance.StartUnshardedKeyspace(*keyspace, 1, false, clusterInstance.Cell)
		if err != nil {
			return 1
		}

		// Run vtgate CPU-constrained, as containerized deployments do. The
		// race needs the canceled checkConn goroutine to be scheduled later
		// than the replacement connection's first health update; a small
		// GOMAXPROCS plus the test's query load gives the Go scheduler a
		// realistic chance to produce that ordering.
		os.Setenv("GOMAXPROCS", "2")
		err = clusterInstance.StartVtgate()
		os.Unsetenv("GOMAXPROCS")
		if err != nil {
			return 1
		}

		vtParams = mysql.ConnParams{
			Host: clusterInstance.Hostname,
			Port: clusterInstance.VtgateMySQLPort,
		}
		return m.Run()
	}()
	os.Exit(exitCode)
}
