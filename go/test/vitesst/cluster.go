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

// Package vitesst provides a testcontainers-based API for spinning up Vitess
// clusters in end-to-end tests. It uses a prebuilt "vitesst:latest" Docker image,
// with each Vitess component running in separate containers on a shared Docker
// network. Make sure to build the image with the current source before running the
// tests in order to ensure the containers are running with the latest code.
//
// Basic usage:
//
//	func TestSomething(t *testing.T) {
//	    cluster := vitesst.NewCluster(t,
//	        vitesst.WithKeyspace("ks").
//	            WithSchema(`CREATE TABLE users (id INT PRIMARY KEY)`).
//	            WithVSchema(`{"sharded": false, "tables": {"users": {}}}`),
//	    )
//
//	    conn := cluster.Connect(t)
//	    defer conn.Close()
//
//	    _, err := conn.ExecuteFetch("INSERT INTO ks.users (id) VALUES (1)", 1, false)
//	    require.NoError(t, err)
//	}
//
// The cluster is automatically cleaned up when the test completes via t.Cleanup().
package vitesst

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	"vitess.io/vitess/go/mysql"
)

type (
	// cluster represents a running Vitess cluster.
	cluster struct {
		opts    *clusterOptions
		network *testcontainers.DockerNetwork
		cells   []string

		// vitessImage is the Docker image name for Vitess components.
		vitessImage string

		etcd      testcontainers.Container
		vtctld    testcontainers.Container
		vtgate    testcontainers.Container
		vtorc     testcontainers.Container
		keyspaces map[string]*keyspaceInfo
	}

	// keyspaceInfo holds runtime information about a keyspace.
	keyspaceInfo struct {
		shards map[string][]*tabletInfo // shard name -> tablets
	}

	// tabletInfo holds runtime information about a tablet.
	tabletInfo struct {
		uid       int
		cell      string
		container testcontainers.Container
	}
)

// NewCluster creates and starts a new Vitess cluster.
// It registers cleanup with t.Cleanup() for automatic teardown.
// Requires at least one keyspace to be configured.
// The cluster uses the prebuilt "vitesst:latest" Docker image.
func NewCluster(t *testing.T, opts ...ClusterOption) *cluster {
	t.Helper()

	// Apply options
	config := defaultClusterOptions()
	for _, opt := range opts {
		opt.apply(config)
	}

	// Validate options
	require.NotEmpty(t, config.keyspaces, "at least one keyspace is required")

	ctx := t.Context()

	c := &cluster{
		opts:        config,
		cells:       config.cells,
		vitessImage: getVitesstImage(),
		keyspaces:   make(map[string]*keyspaceInfo),
	}

	// Register cleanup
	t.Cleanup(func() {
		c.cleanup(t)
	})

	var err error

	// Create network
	c.network, err = createNetwork(ctx)
	require.NoError(t, err, "failed to create network")

	// Start etcd
	t.Log("Starting etcd...")
	c.etcd, err = c.startEtcd(ctx)
	require.NoError(t, err, "failed to start etcd")

	// Start vtctld
	t.Log("Starting vtctld...")
	c.vtctld, err = c.startVTCtld(ctx)
	require.NoError(t, err, "failed to start vtctld")

	// Initialize cells
	for _, cell := range c.cells {
		err = c.initCell(ctx, cell)
		require.NoError(t, err, "failed to initialize cell %s", cell)
	}

	// Set up keyspaces
	tabletUID := 100
	for _, ks := range config.keyspaces {
		tabletUID, err = c.setupKeyspace(t, ks, tabletUID)
		require.NoError(t, err, "failed to setup keyspace %s", ks.name)
	}

	// Start VTOrc if enabled
	if config.vtorcEnabled {
		t.Log("Starting VTOrc...")
		c.vtorc, err = c.startVTOrc(ctx)
		require.NoError(t, err, "failed to start VTOrc")
	}

	// Start vtgate
	t.Log("Starting vtgate...")
	c.vtgate, err = c.startVTGate(ctx)
	require.NoError(t, err, "failed to start vtgate")

	t.Log("Vitess cluster is ready")
	return c
}

// Connect returns a new MySQL connection to vtgate.
//
//	conn := cluster.Connect(t)
//	defer conn.Close()
func (c *cluster) Connect(t *testing.T) *mysql.Conn {
	t.Helper()

	conn, err := c.connect(t.Context(), "")
	require.NoError(t, err, "failed to connect to vtgate")

	return conn
}

// ConnectKeyspace returns a new connection targeting a specific keyspace.
//
//	conn := cluster.ConnectKeyspace(t, "ks")
//	defer conn.Close()
func (c *cluster) ConnectKeyspace(t *testing.T, keyspace string) *mysql.Conn {
	t.Helper()

	conn, err := c.connect(t.Context(), keyspace)
	require.NoError(t, err, "failed to connect to keyspace %s", keyspace)

	return conn
}

// connect creates a MySQL connection to vtgate.
func (c *cluster) connect(ctx context.Context, keyspace string) (*mysql.Conn, error) {
	host, err := c.vtgate.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get vtgate host: %w", err)
	}

	port, err := c.vtgate.MappedPort(ctx, "15306/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get vtgate port: %w", err)
	}

	params := mysql.ConnParams{Host: host, Port: port.Int(), DbName: keyspace}

	conn, err := mysql.Connect(ctx, &params)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return conn, nil
}

// setupKeyspace sets up a keyspace with tablets and schema.
func (c *cluster) setupKeyspace(t *testing.T, ks keyspaceConfig, startUID int) (int, error) {
	t.Helper()
	ctx := t.Context()

	t.Logf("Setting up keyspace %s...", ks.name)

	// Apply defaults
	if ks.shardCount == 0 {
		ks.shardCount = defaultShardCount
	}

	if ks.replicaCount == 0 {
		ks.replicaCount = defaultReplicaCount
	}

	if ks.durabilityPolicy == "" {
		ks.durabilityPolicy = defaultDurabilityPolicy
	}

	// Create keyspace
	if err := c.vtctldExec(ctx, "CreateKeyspace", "--durability-policy", ks.durabilityPolicy, ks.name); err != nil {
		return startUID, fmt.Errorf("failed to create keyspace: %w", err)
	}

	// Generate shard ranges
	shardRanges, err := generateShardRanges(ks.shardCount)
	if err != nil {
		return startUID, fmt.Errorf("failed to generate shard ranges: %w", err)
	}

	// Initialize keyspace info
	ksInfo := &keyspaceInfo{shards: make(map[string][]*tabletInfo)}
	c.keyspaces[ks.name] = ksInfo

	// Start tablets for each shard, distributing across cells round-robin
	tabletUID := startUID
	cellIndex := 0
	for _, shard := range shardRanges {
		// Track the first tablet for this shard (will be the primary)
		var primaryTablet *tabletInfo

		// replicaCount includes the primary:
		// 1 = primary only, 2 = primary + 1 replica, etc.
		for i := 0; i < ks.replicaCount; i++ {
			cell := c.cells[cellIndex%len(c.cells)]
			cellIndex++

			t.Logf("Starting tablet for %s/%s (uid=%d, cell=%s)...", ks.name, shard, tabletUID, cell)

			container, err := c.startVTTablet(ctx, ks.name, shard, tabletUID, cell)
			if err != nil {
				return tabletUID, fmt.Errorf("failed to start tablet for shard %s: %w", shard, err)
			}
			tablet := &tabletInfo{uid: tabletUID, cell: cell, container: container}
			ksInfo.shards[shard] = append(ksInfo.shards[shard], tablet)
			if primaryTablet == nil {
				primaryTablet = tablet
			}
			tabletUID++
		}

		// Initialize shard primary
		t.Logf("Initializing shard primary for %s/%s...", ks.name, shard)
		tabletAlias := fmt.Sprintf("%s-%d", primaryTablet.cell, primaryTablet.uid)
		if err := c.vtctldExec(ctx, "InitShardPrimary", "--force", "--wait-replicas-timeout", "30s", ks.name+"/"+shard, tabletAlias); err != nil {
			return tabletUID, fmt.Errorf("failed to initialize shard primary: %w", err)
		}
	}

	// Apply schema
	if ks.schema != "" {
		t.Logf("Applying schema to keyspace %s...", ks.name)
		if err := c.vtctldExec(ctx, "ApplySchema", "--sql", ks.schema, ks.name); err != nil {
			return tabletUID, fmt.Errorf("failed to apply schema: %w", err)
		}
	}

	// Apply VSchema
	if ks.vschema != "" {
		t.Logf("Applying VSchema to keyspace %s...", ks.name)
		if err := c.vtctldExec(ctx, "ApplyVSchema", "--vschema", ks.vschema, ks.name); err != nil {
			return tabletUID, fmt.Errorf("failed to apply VSchema: %w", err)
		}
	}

	return tabletUID, nil
}

// String returns connection information for all cluster components.
func (c *cluster) String() string {
	ctx := context.Background()

	var sb strings.Builder
	sb.WriteString("\n" + strings.Repeat("=", 60) + "\n")
	sb.WriteString("VITESS CLUSTER CONNECTION INFO\n")
	sb.WriteString(strings.Repeat("=", 60) + "\n")

	// VTGate
	if c.vtgate != nil {
		host, _ := c.vtgate.Host(ctx)
		mysqlPort, _ := c.vtgate.MappedPort(ctx, "15306/tcp")
		httpPort, _ := c.vtgate.MappedPort(ctx, "15001/tcp")
		grpcPort, _ := c.vtgate.MappedPort(ctx, "15999/tcp")

		sb.WriteString("\nVTGate:\n")
		fmt.Fprintf(&sb, "  MySQL:  mysql -h %s -P %d\n", host, mysqlPort.Int())
		fmt.Fprintf(&sb, "  HTTP:   http://%s:%d\n", host, httpPort.Int())
		fmt.Fprintf(&sb, "  gRPC:   %s:%d\n", host, grpcPort.Int())
	}

	// VTCtld
	if c.vtctld != nil {
		host, _ := c.vtctld.Host(ctx)
		httpPort, _ := c.vtctld.MappedPort(ctx, "15000/tcp")
		grpcPort, _ := c.vtctld.MappedPort(ctx, "15999/tcp")

		sb.WriteString("\nVTCtld:\n")
		fmt.Fprintf(&sb, "  HTTP:   http://%s:%d\n", host, httpPort.Int())
		fmt.Fprintf(&sb, "  gRPC:   %s:%d\n", host, grpcPort.Int())
		fmt.Fprintf(&sb, "  CLI:    vtctldclient --server %s:%d <command>\n", host, grpcPort.Int())
	}

	// VTOrc
	if c.vtorc != nil {
		host, _ := c.vtorc.Host(ctx)
		httpPort, _ := c.vtorc.MappedPort(ctx, "16000/tcp")

		sb.WriteString("\nVTOrc:\n")
		fmt.Fprintf(&sb, "  HTTP:   http://%s:%d\n", host, httpPort.Int())
		fmt.Fprintf(&sb, "  Health: http://%s:%d/debug/health\n", host, httpPort.Int())
	}

	// Keyspaces with shards and tablets
	if len(c.keyspaces) > 0 {
		sb.WriteString("\nKeyspaces:\n")
		for _, ks := range c.opts.keyspaces {
			ksInfo := c.keyspaces[ks.name]
			if ksInfo == nil {
				continue
			}
			fmt.Fprintf(&sb, "  %s:\n", ks.name)

			shardCount := ks.shardCount
			if shardCount == 0 {
				shardCount = defaultShardCount
			}
			shards, _ := generateShardRanges(shardCount)
			for _, shard := range shards {
				fmt.Fprintf(&sb, "    Shard %s:\n", shard)
				for _, tablet := range ksInfo.shards[shard] {
					host, _ := tablet.container.Host(ctx)
					mysqlPort, _ := tablet.container.MappedPort(ctx, "3306/tcp")
					fmt.Fprintf(&sb, "      tablet-%d: mysql -h %s -P %d\n", tablet.uid, host, mysqlPort.Int())
				}
			}
		}
	}

	sb.WriteString(strings.Repeat("=", 60) + "\n")
	return sb.String()
}

// cleanup terminates all containers and the network.
func (c *cluster) cleanup(t *testing.T) {
	t.Helper()

	ctx := context.Background()

	for _, container := range []testcontainers.Container{c.vtgate, c.vtorc, c.vtctld, c.etcd} {
		if container != nil {
			if err := container.Terminate(ctx); err != nil {
				t.Logf("Warning: failed to terminate container: %v", err)
			}
		}
	}

	for _, ksInfo := range c.keyspaces {
		for _, tablets := range ksInfo.shards {
			for _, tablet := range tablets {
				if err := tablet.container.Terminate(ctx); err != nil {
					t.Logf("Warning: failed to terminate tablet: %v", err)
				}
			}
		}
	}

	if c.network != nil {
		if err := c.network.Remove(ctx); err != nil {
			t.Logf("Warning: failed to remove network: %v", err)
		}
	}
}

// getVitesstImage returns the Vitess Docker image name.
// It uses the VITESST_IMAGE environment variable if set, otherwise returns the default.
func getVitesstImage() string {
	if image := os.Getenv("VITESST_IMAGE"); image != "" {
		return image
	}

	return vitesstImage
}
