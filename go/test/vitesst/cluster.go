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

// Cluster represents a running Vitess cluster.
type Cluster struct {
	opts    *clusterOptions
	network *testcontainers.DockerNetwork
	cell    string

	// vitessImage is the Docker image name for Vitess components.
	vitessImage string

	etcd    testcontainers.Container
	vtctld  testcontainers.Container
	vtgate  testcontainers.Container
	tablets []testcontainers.Container
}

// NewCluster creates and starts a new Vitess cluster.
// It registers cleanup with t.Cleanup() for automatic teardown.
// Requires at least one keyspace to be configured.
// The cluster uses the prebuilt "vitesst:latest" Docker image.
func NewCluster(t *testing.T, opts ...ClusterOption) *Cluster {
	t.Helper()

	// Apply options
	clusterOpts := defaultClusterOptions()
	for _, opt := range opts {
		opt.apply(clusterOpts)
	}

	// Validate options
	require.NotEmpty(t, clusterOpts.keyspaces, "at least one keyspace is required")

	ctx := t.Context()

	c := &Cluster{
		opts:        clusterOpts,
		cell:        clusterOpts.cell,
		vitessImage: getVitesstImage(),
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

	// Initialize cell
	err = c.initCell(ctx)
	require.NoError(t, err, "failed to initialize cell")

	// Set up keyspaces
	tabletUID := 100
	for _, ks := range clusterOpts.keyspaces {
		tabletUID, err = c.setupKeyspace(t, ks, tabletUID)
		require.NoError(t, err, "failed to setup keyspace %s", ks.name)
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
func (c *Cluster) Connect(t *testing.T) *mysql.Conn {
	t.Helper()

	conn, err := c.connect(t.Context(), "")
	require.NoError(t, err, "failed to connect to vtgate")

	return conn
}

// ConnectKeyspace returns a new connection targeting a specific keyspace.
//
//	conn := cluster.ConnectKeyspace(t, "ks")
//	defer conn.Close()
func (c *Cluster) ConnectKeyspace(t *testing.T, keyspace string) *mysql.Conn {
	t.Helper()

	conn, err := c.connect(t.Context(), keyspace)
	require.NoError(t, err, "failed to connect to keyspace %s", keyspace)

	return conn
}

// connect creates a MySQL connection to vtgate.
func (c *Cluster) connect(ctx context.Context, keyspace string) (*mysql.Conn, error) {
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
func (c *Cluster) setupKeyspace(t *testing.T, ks keyspaceConfig, startUID int) (int, error) {
	t.Helper()
	ctx := t.Context()

	t.Logf("Setting up keyspace %s...", ks.name)

	// Apply defaults
	if ks.shardCount == 0 {
		ks.shardCount = DefaultShardCount
	}

	if ks.replicaCount == 0 {
		ks.replicaCount = DefaultReplicaCount
	}

	if ks.durabilityPolicy == "" {
		ks.durabilityPolicy = DefaultDurabilityPolicy
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

	// Start tablets for each shard
	tabletUID := startUID
	for _, shard := range shardRanges {
		// replicaCount includes the primary:
		// 1 = primary only, 2 = primary + 1 replica, etc.
		for i := 0; i < ks.replicaCount; i++ {
			t.Logf("Starting tablet for %s/%s (uid=%d)...", ks.name, shard, tabletUID)

			tablet, err := c.startVTTablet(ctx, ks.name, shard, tabletUID)
			if err != nil {
				return tabletUID, fmt.Errorf("failed to start tablet for shard %s: %w", shard, err)
			}
			c.tablets = append(c.tablets, tablet)

			// Initialize shard primary on the first tablet
			if i == 0 {
				t.Logf("Initializing shard primary for %s/%s...", ks.name, shard)
				tabletAlias := fmt.Sprintf("%s-%d", c.cell, tabletUID)
				if err := c.vtctldExec(ctx, "InitShardPrimary", "--force", ks.name+"/"+shard, tabletAlias); err != nil {
					return tabletUID, fmt.Errorf("failed to initialize shard primary: %w", err)
				}
			}

			tabletUID++
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
func (c *Cluster) String(t *testing.T) string {
	ctx := t.Context()

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

	// Tablets
	if len(c.tablets) > 0 {
		sb.WriteString("\nTablets:\n")
		for i, tablet := range c.tablets {
			host, _ := tablet.Host(ctx)
			mysqlPort, _ := tablet.MappedPort(ctx, "3306/tcp")

			fmt.Fprintf(&sb, "  Tablet %d:\n", i)
			fmt.Fprintf(&sb, "    MySQL: mysql -h %s -P %d\n", host, mysqlPort.Int())
		}
	}

	// Keyspaces
	if len(c.opts.keyspaces) > 0 {
		sb.WriteString("\nKeyspaces:\n")
		for _, ks := range c.opts.keyspaces {
			fmt.Fprintf(&sb, "  - %s\n", ks.name)
		}
	}

	sb.WriteString(strings.Repeat("=", 60) + "\n")
	return sb.String()
}

// cleanup terminates all containers and the network.
func (c *Cluster) cleanup(t *testing.T) {
	ctx := context.Background()

	for _, container := range []testcontainers.Container{c.vtgate, c.vtctld, c.etcd} {
		if container != nil {
			if err := container.Terminate(ctx); err != nil {
				t.Logf("Warning: failed to terminate container: %v", err)
			}
		}
	}

	for _, tablet := range c.tablets {
		if err := tablet.Terminate(ctx); err != nil {
			t.Logf("Warning: failed to terminate tablet: %v", err)
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
