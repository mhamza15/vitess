/*
Copyright 2025 The Vitess Authors.

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

package vitesst

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startTablets starts all tablets for a keyspace in parallel and elects primaries.
func (c *cluster) startTablets(t *testing.T, wg *sync.WaitGroup, ks keyspaceConfig, startUID int) int {
	t.Helper()

	shardRanges, _ := generateShardRanges(ks.shardCount)
	ksInfo := c.keyspaces[ks.name]

	tabletUID := startUID
	cellIndex := 0
	for _, shard := range shardRanges {
		t.Logf("Starting tablets for shard %q...", shard)

		var shardWg sync.WaitGroup
		for i := range ks.replicaCount {
			cell := c.cells[cellIndex%len(c.cells)]
			uid := tabletUID
			idx := i

			shardWg.Go(func() {
				t.Logf("Starting tablet %s-%d", cell, uid)

				container, err := c.startVTTablet(t, ks.name, shard, uid, cell)
				require.NoError(t, err)

				ksInfo.shards[shard][idx] = &tabletInfo{uid: uid, cell: cell, container: container}
			})

			cellIndex++
			tabletUID++
		}

		// Elect primary for this shard after all its tablets are up
		wg.Go(func() {
			shardWg.Wait()

			// Skip primary election if the test has already failed (e.g., due to tablet startup failure)
			if t.Failed() {
				return
			}

			t.Logf("Electing primary for shard %q...", shard)

			primary := ksInfo.shards[shard][0]
			if primary == nil {
				return
			}
			alias := fmt.Sprintf("%s-%d", primary.cell, primary.uid)
			err := c.vtctldExec(t, "PlannedReparentShard", "--new-primary", alias, ks.name+"/"+shard)
			require.NoError(t, err)
		})
	}

	return tabletUID
}

// startVTTablet starts a vttablet container with MySQL in the specified cell.
func (c *cluster) startVTTablet(t *testing.T, keyspace, shard string, uid int, cell string) (testcontainers.Container, error) {
	httpPort := 15100 + uid
	grpcPort := 16100 + uid
	alias := fmt.Sprintf("vttablet-%s-%s-%d", keyspace, shard, uid)

	startupScript := fmt.Sprintf(`#!/bin/bash
set -ex
mysqlctl --tablet-uid %d --mysql-port 3306 init
exec vttablet \
  --topo-implementation %s \
  --topo-global-server-address etcd:2379 \
  --topo-global-root %s \
  --tablet-path %s-%d \
  --init-keyspace %s \
  --init-shard %s \
  --init-tablet-type replica \
  --port %d \
  --grpc-port %d \
  --service-map 'grpc-queryservice,grpc-tabletmanager,grpc-updatestream' \
  --enable-replication-reporter \
  %s
`, uid, defaultTopoImplementation, topoGlobalRoot, cell, uid, keyspace, shard, httpPort, grpcPort, strings.Join(c.opts.vttabletArgs, " "))

	return testcontainers.Run(t.Context(), c.vitesstImage,
		testcontainers.WithEntrypoint("bash", "-c", startupScript),
		testcontainers.WithExposedPorts(
			fmt.Sprintf("%d/tcp", httpPort),
			fmt.Sprintf("%d/tcp", grpcPort),
			"3306/tcp",
		),
		network.WithNetwork([]string{alias}, c.network),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/debug/status").
				WithPort(nat.Port(fmt.Sprintf("%d/tcp", httpPort))).
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval),
		),
		testcontainers.WithLogger(log.TestLogger(t)),
	)
}
