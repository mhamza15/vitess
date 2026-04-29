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
// clusters in end-to-end tests. It uses prebuilt vitesst:mysql80 and
// vitesst:mysql84 Docker images, with each Vitess component running in separate
// containers on a shared Docker network. Build the images from the current
// source before running tests so containers use the code under test.
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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	"vitess.io/vitess/go/mysql"
)

type (
	// Cluster represents a running Vitess cluster.
	Cluster struct {
		// opts keeps the validated cluster configuration for helpers that
		// start or inspect components after NewCluster returns.
		opts *clusterOptions

		// network isolates containers from parallel tests and gives them stable
		// aliases for intra-cluster communication.
		network *testcontainers.DockerNetwork

		// cells lists the topo cells used when tablets are distributed across
		// the cluster.
		cells []string

		// vitesstImage is the Docker image name for Vitess components.
		vitesstImage string

		// etcd is the topology server shared by all Vitess components.
		etcd testcontainers.Container

		// vtctld is used to initialize topology and run vtctldclient commands.
		vtctld testcontainers.Container

		// vtgate serves client MySQL connections for the test cluster.
		vtgate testcontainers.Container

		// vtorc is present only when the test enables VTOrc.
		vtorc testcontainers.Container

		// keyspaces records tablet containers by keyspace and shard.
		keyspaces map[string]*keyspaceInfo
	}

	// keyspaceInfo holds runtime information about a keyspace.
	keyspaceInfo struct {
		// shards maps each shard name to the tablets that serve it.
		shards map[string][]TabletInfo
	}
)

// NewCluster creates and starts a new Vitess cluster.
// It registers cleanup with t.Cleanup() for automatic teardown.
// Requires at least one keyspace to be configured.
// The cluster uses the prebuilt vitesst image matching the configured MySQL version.
func NewCluster(t *testing.T, opts ...ClusterOption) *Cluster {
	t.Helper()

	config := buildConfig(t, opts)

	c := &Cluster{
		opts:         config,
		cells:        config.cells,
		vitesstImage: getVitesstImage(config.mysqlVersion),
		keyspaces:    make(map[string]*keyspaceInfo),
	}

	t.Cleanup(func() { c.cleanup(t) })

	var err error
	c.network, err = createNetwork(t)
	require.NoError(t, err)

	c.etcd, err = c.startEtcd(t)
	require.NoError(t, err)

	c.vtctld, err = c.startVTCtld(t)
	require.NoError(t, err)

	for _, cell := range c.cells {
		require.NoError(t, c.initCell(t, cell))
	}

	// Create keyspaces in topology
	c.createKeyspaces(t, config.keyspaces)

	// Start tablets in each keyspace concurrently
	var tabletsWg sync.WaitGroup

	tabletUID := 100
	for _, ks := range config.keyspaces {
		tabletUID = c.startTablets(t, &tabletsWg, ks, tabletUID)
	}

	tabletsWg.Wait()

	// Start VTGate and VTOrc (if enabled) concurrently
	var wg sync.WaitGroup

	// Start VTGate
	wg.Go(func() {
		container, err := c.startVTGate(t)
		require.NoError(t, err)
		c.vtgate = container
	})

	// Start VTOrc if enabled
	if config.vtorcEnabled {
		wg.Go(func() {
			container, err := c.startVTOrc(t)
			require.NoError(t, err)
			c.vtorc = container
		})
	}

	wg.Wait()

	// Apply initial schema
	for _, ks := range config.keyspaces {
		if ks.schema != "" {
			err := c.vtctldExec(t, "ApplySchema", "--sql", ks.schema, ks.name)
			require.NoError(t, err)
		}
	}

	log(t, "Vitess cluster is ready")
	return c
}

func buildConfig(t *testing.T, opts []ClusterOption) *clusterOptions {
	t.Helper()

	config := defaultClusterOptions()
	for _, opt := range opts {
		opt.apply(config)
	}

	require.NotEmpty(t, config.keyspaces, "at least one keyspace is required")
	require.NotEmpty(t, config.cells, "at least one cell is required")

	for _, cell := range config.cells {
		require.NotEmpty(t, cell, "cell names must not be empty")
	}

	switch config.mysqlVersion {
	case "8.0", "8.4":
	default:
		t.Fatalf("unsupported MySQL version %q, supported versions are 8.0 and 8.4", config.mysqlVersion)
	}

	return config
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

// Tablets returns all tablets in the cluster.
func (c *Cluster) Tablets() []TabletInfo {
	var tablets []TabletInfo
	for _, ksInfo := range c.keyspaces {
		for _, shardTablets := range ksInfo.shards {
			tablets = append(tablets, shardTablets...)
		}
	}
	return tablets
}

// TabletsKeyspace returns all tablets for a given keyspace.
func (c *Cluster) TabletsKeyspace(keyspace string) []TabletInfo {
	ksInfo, ok := c.keyspaces[keyspace]
	if !ok {
		return nil
	}

	var tablets []TabletInfo
	for _, shardTablets := range ksInfo.shards {
		tablets = append(tablets, shardTablets...)
	}
	return tablets
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

// applyKeyspaceDefaults fills in default values for keyspace config.
func (c *Cluster) applyKeyspaceDefaults(ks *keyspaceConfig) {
	if ks.shardCount == 0 {
		ks.shardCount = defaultShardCount
	}

	if ks.replicaCount == 0 {
		ks.replicaCount = defaultReplicaCount
	}

	if ks.durabilityPolicy == "" {
		ks.durabilityPolicy = defaultDurabilityPolicy
	}
}

// createKeyspaces creates keyspaces in topology and initializes shard info.
func (c *Cluster) createKeyspaces(t *testing.T, keyspaces []keyspaceConfig) {
	t.Helper()

	for i := range keyspaces {
		ks := &keyspaces[i]
		c.applyKeyspaceDefaults(ks)

		require.NoError(t, c.vtctldExec(t, "CreateKeyspace", "--durability-policy", ks.durabilityPolicy, ks.name))

		if ks.vschema != "" {
			require.NoError(t, c.vtctldExec(t, "ApplyVSchema", "--vschema", ks.vschema, ks.name))
		}

		shardRanges, err := generateShardRanges(ks.shardCount)
		require.NoError(t, err)

		ksInfo := &keyspaceInfo{shards: make(map[string][]TabletInfo)}
		for _, shard := range shardRanges {
			ksInfo.shards[shard] = make([]TabletInfo, ks.replicaCount)
		}
		c.keyspaces[ks.name] = ksInfo
	}
}

// String returns connection information for all cluster components.
func (c *Cluster) String() string {
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
					fmt.Fprintf(&sb, "      tablet-%d: mysql -h %s -P %d\n", tablet.UID, host, mysqlPort.Int())
				}
			}
		}
	}

	sb.WriteString(strings.Repeat("=", 60) + "\n")
	return sb.String()
}

// cleanup terminates all containers and the network.
func (c *Cluster) cleanup(t *testing.T) {
	t.Helper()

	ctx := context.Background()

	var wg sync.WaitGroup
	for _, container := range []testcontainers.Container{c.vtgate, c.vtorc, c.vtctld, c.etcd} {
		if container != nil {
			wg.Go(func() {
				if err := container.Terminate(ctx, testcontainers.StopTimeout(0)); err != nil {
					t.Logf("Warning: failed to terminate container: %v", err)
				}
			})
		}
	}

	for _, ksInfo := range c.keyspaces {
		for _, tablets := range ksInfo.shards {
			for _, tablet := range tablets {
				if tablet.container == nil {
					continue
				}
				wg.Go(func() {
					if err := tablet.container.Terminate(ctx, testcontainers.StopTimeout(0)); err != nil {
						t.Logf("Warning: failed to terminate tablet: %v", err)
					}
				})
			}
		}
	}
	wg.Wait()

	if c.network != nil {
		if err := c.network.Remove(ctx); err != nil {
			t.Logf("Warning: failed to remove network: %v", err)
		}
	}
}

// getVitesstImage returns the Vitess Docker image name.
// It uses the VITESST_IMAGE environment variable if set, otherwise constructs
// an image name based on the MySQL version (e.g., "vitesst:mysql84").
func getVitesstImage(mysqlVersion string) string {
	if image := os.Getenv("VITESST_IMAGE"); image != "" {
		return image
	}

	// Convert version like "8.4" to tag like "mysql84"
	tag := "mysql" + strings.ReplaceAll(mysqlVersion, ".", "")
	return vitesstImage + ":" + tag
}
