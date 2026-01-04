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
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"vitess.io/vitess/go/mysql"
	"vitess.io/vitess/go/sqltypes"
)

type (
	vttabletLogFollowingOption struct{}

	// TabletInfo holds runtime information about a tablet.
	TabletInfo struct {
		UID       int
		Cell      string
		Keyspace  string
		Shard     string
		container testcontainers.Container
	}
)

func (vttabletLogFollowingOption) apply(opts *clusterOptions) {
	opts.vttabletLogFollowing = true
}

// WithVTTabletLogger enables following vttablet container logs to test output.
func WithVTTabletLogger() ClusterOption {
	return vttabletLogFollowingOption{}
}

// Alias returns the tablet alias in the format "cell-uid" (e.g., "zone1-100").
func (t TabletInfo) Alias() string {
	return fmt.Sprintf("%s-%d", t.Cell, t.UID)
}

// QueryTablet executes a query directly on the tablet's MySQL instance,
// bypassing vtgate.
func (t TabletInfo) QueryTablet(ctx context.Context, query string) (*sqltypes.Result, error) {
	return t.QueryTabletWithDB(ctx, query, "vt_"+t.Keyspace)
}

// QueryTabletWithDB executes a query against a specific database on the tablet.
// Use dbName="" to execute without selecting a database.
func (t TabletInfo) QueryTabletWithDB(ctx context.Context, query, dbName string) (*sqltypes.Result, error) {
	host, err := t.container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tablet host: %w", err)
	}

	port, err := t.container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get tablet MySQL port: %w", err)
	}

	params := mysql.ConnParams{
		Host:   host,
		Port:   port.Int(),
		Uname:  "vt_dba",
		DbName: dbName,
	}

	conn, err := mysql.Connect(ctx, &params)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tablet: %w", err)
	}
	defer conn.Close()

	return conn.ExecuteFetch(query, 10000, true)
}

// Kill immediately stops the tablet container.
func (t TabletInfo) Kill(ctx context.Context) error {
	return t.Stop(ctx, 0)
}

// Stop gracefully stops the tablet container with a timeout.
// If the container doesn't stop within the timeout, it's killed.
func (t TabletInfo) Stop(ctx context.Context, timeout time.Duration) error {
	return t.container.Stop(ctx, &timeout)
}

// Start restarts a stopped tablet container.
func (t TabletInfo) Start(ctx context.Context) error {
	return t.container.Start(ctx)
}

// IsRunning returns true if the tablet container is running.
func (t TabletInfo) IsRunning() bool {
	return t.container.IsRunning()
}

// startTablets starts all tablets for a keyspace in parallel and elects primaries.
func (c *Cluster) startTablets(t *testing.T, wg *sync.WaitGroup, ks keyspaceConfig, startUID int) int {
	t.Helper()

	shardRanges, _ := generateShardRanges(ks.shardCount)
	ksInfo := c.keyspaces[ks.name]

	tabletUID := startUID
	cellIndex := 0
	for _, shard := range shardRanges {
		log(t, "Starting tablets for shard %q...", shard)

		var shardWg sync.WaitGroup
		for i := range ks.replicaCount {
			cell := c.cells[cellIndex%len(c.cells)]
			uid := tabletUID
			idx := i

			shardWg.Go(func() {
				log(t, "Starting tablet %s-%d", cell, uid)

				container, err := c.startVTTablet(t, ks.name, shard, uid, cell)
				require.NoError(t, err)

				ksInfo.shards[shard][idx] = TabletInfo{
					UID:       uid,
					Cell:      cell,
					Keyspace:  ks.name,
					Shard:     shard,
					container: container,
				}
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

			log(t, "Electing primary for shard %q...", shard)

			primary := ksInfo.shards[shard][0]
			if primary.container == nil {
				return
			}
			err := c.vtctldExec(t, "PlannedReparentShard", "--new-primary", primary.Alias(), ks.name+"/"+shard)
			require.NoError(t, err)
		})
	}

	return tabletUID
}

// startVTTablet starts a vttablet container with MySQL in the specified cell.
func (c *Cluster) startVTTablet(t *testing.T, keyspace, shard string, uid int, cell string) (testcontainers.Container, error) {
	httpPort := 15100 + uid
	grpcPort := 16100 + uid
	alias := fmt.Sprintf("vttablet-%s-%s-%d", keyspace, shard, uid)

	startupScript := fmt.Sprintf(`#!/bin/bash
set -ex
mysqlctl --tablet-uid %d --mysql-port 3306 --init-db-sql-file /vt/config/init_db.sql init

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

	containerOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithEntrypoint("bash", "-c", startupScript),
		testcontainers.WithExposedPorts(
			fmt.Sprintf("%d/tcp", httpPort),
			fmt.Sprintf("%d/tcp", grpcPort),
			"3306/tcp",
		),
		network.WithNetwork([]string{alias}, c.network),
		testcontainers.WithTmpfs(map[string]string{"/vt/vtdataroot": "uid=999,gid=999"}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/debug/status").
				WithPort(nat.Port(fmt.Sprintf("%d/tcp", httpPort))).
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval),
		),
	}

	if c.opts.vttabletLogFollowing {
		containerOpts = append(containerOpts, testcontainers.WithLogConsumers(&testLogConsumer{prefix: alias}))
	}

	return testcontainers.Run(t.Context(), c.vitesstImage, containerOpts...)
}
