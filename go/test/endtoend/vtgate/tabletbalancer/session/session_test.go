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

package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"vitess.io/vitess/go/mysql"
	"vitess.io/vitess/go/test/vitesst"
)

const (
	sessionKeyspaceName = "ks"

	sessionSchemaSQL = `create table balancer_test(
		id bigint not null auto_increment,
		value varchar(128),
		primary key(id)
	) ENGINE=InnoDB;`

	sessionVSchema = `{
		"sharded": false,
		"tables": {
			"balancer_test": {}
		}
	}`
)

// TestSessionModeBalancer verifies that session mode routes each session
// consistently to the same tablet.
func TestSessionModeBalancer(t *testing.T) {
	t.Parallel()

	cluster, _ := setupCluster(t)

	// Use two tablets so the test proves stickiness without depending on one
	// tablet receiving all traffic by chance.
	conns := createSessionConnections(t, cluster, 2)
	for conn := range conns {
		defer conn.Close()
	}

	verifyStickiness(t, conns, 20)
}

// TestSessionModeRemoveTablet verifies that vtgate moves a session connection
// away from a tablet after that tablet is killed.
func TestSessionModeRemoveTablet(t *testing.T) {
	t.Parallel()

	cluster, aliases := setupCluster(t)

	// Keep two distinct sessions so the killed tablet is known to be serving
	// at least one connection.
	conns := createSessionConnections(t, cluster, 2)
	for conn := range conns {
		defer conn.Close()
	}

	tablets := cluster.Tablets()

	var tabletToKill *vitesst.TabletInfo
	var affectedConn *mysql.Conn
	var killedServerID int64

	for _, tablet := range tablets {
		tabletServerID := aliases[tablet.Alias()]

		for conn, connServerID := range conns {
			if connServerID != tabletServerID {
				continue
			}

			tabletToKill = &tablet
			affectedConn = conn
			killedServerID = tabletServerID
			break
		}

		if tabletToKill != nil {
			break
		}
	}

	require.NotNil(t, tabletToKill, "Should find a tablet to kill")

	err := tabletToKill.Kill(t.Context())
	require.NoError(t, err)

	// vtgate learns about the unhealthy tablet asynchronously through health
	// checks, so give the session balancer a realistic failover window.
	require.Eventually(t, func() bool {
		newServerID := getServerID(t, affectedConn)
		if newServerID != killedServerID {
			conns[affectedConn] = newServerID
			return true
		}

		return false
	}, 30*time.Second, 100*time.Millisecond, "Connection should switch to a different tablet")

	verifyStickiness(t, conns, 20)
}

// setupCluster sets up a cluster with a vtgate using the session balancer.
// Matches the old test setup: 2 cells (zone1, zone2) with 6 tablets total
// distributed round-robin across cells.
func setupCluster(t *testing.T) (*vitesst.Cluster, map[string]int64) {
	t.Helper()

	cluster := vitesst.NewCluster(t,
		vitesst.WithCells("zone1", "zone2"),

		vitesst.WithKeyspace(sessionKeyspaceName).
			WithSchema(sessionSchemaSQL).
			WithVSchema(sessionVSchema).
			WithReplicaCount(6),

		vitesst.WithVTGateArgs("--vtgate-balancer-mode", "session"),
	)

	tablets := cluster.Tablets()
	aliases := mapTabletAliasToServerID(t, tablets)

	// The replication wait needs a row that was written after the cluster was
	// created so every replica proves it has received fresh data.
	conn := cluster.Connect(t)
	defer conn.Close()

	testValue := fmt.Sprintf("session_test_%d", time.Now().UnixNano())
	_, err := conn.ExecuteFetch(fmt.Sprintf("INSERT INTO balancer_test (value) VALUES ('%s')", testValue), 1, false)
	require.NoError(t, err)

	sessionWaitForReplication(t, tablets, testValue)

	return cluster, aliases
}

// getServerID returns the server ID that the connection is currently routing to.
func getServerID(t *testing.T, conn *mysql.Conn) int64 {
	t.Helper()

	res, err := conn.ExecuteFetch("SELECT @@server_id", 1, false)
	require.NoError(t, err)
	require.Equal(t, 1, len(res.Rows), "expected one row from server_id query")

	serverID, err := res.Rows[0][0].ToInt64()
	require.NoError(t, err)

	return serverID
}

// createSessionConnections creates connections that route to different tablets.
// It returns the server id observed by each connection so later assertions can
// verify that vtgate keeps routing the session to the same backing tablet.
func createSessionConnections(t *testing.T, cluster *vitesst.Cluster, numConnections int) map[*mysql.Conn]int64 {
	t.Helper()

	conns := make(map[*mysql.Conn]int64)
	seenServerIDs := make(map[int64]bool)

	// Retry because vtgate chooses replicas independently and may return a
	// tablet we already sampled.
	for range 50 {
		conn := cluster.ConnectKeyspace(t, sessionKeyspaceName+"@replica")

		serverID := getServerID(t, conn)

		if !seenServerIDs[serverID] {
			seenServerIDs[serverID] = true
			conns[conn] = serverID

			if len(conns) == numConnections {
				return conns
			}

			continue
		}

		conn.Close()
	}

	t.Fatalf("could not create %d connections with different tablets after 50 attempts, only got %d", numConnections, len(conns))
	return nil
}

// verifyStickiness validates whether the given connections remain connected to the same
// server `n` times in a row.
func verifyStickiness(t *testing.T, conns map[*mysql.Conn]int64, n uint) {
	t.Helper()

	for conn, expectedServerID := range conns {
		for range n {
			currentServerID := getServerID(t, conn)
			require.Equal(t, expectedServerID, currentServerID, "Connection should stick to tablet %d, got %d", expectedServerID, currentServerID)
		}
	}
}

// mapTabletAliasToServerID queries each tablet to get its MySQL server_id and returns a map.
func mapTabletAliasToServerID(t *testing.T, tablets []vitesst.TabletInfo) map[string]int64 {
	t.Helper()

	aliases := make(map[string]int64)
	ctx := t.Context()

	for _, tablet := range tablets {
		res, err := tablet.QueryTabletWithDB(ctx, "SELECT @@server_id", "")
		require.NoError(t, err)
		require.Equal(t, 1, len(res.Rows), "expected one row for server_id query")

		serverID, err := res.Rows[0][0].ToInt64()
		require.NoError(t, err)

		aliases[tablet.Alias()] = serverID
	}

	return aliases
}

// sessionWaitForReplication waits for a specific value to be replicated to all tablets.
func sessionWaitForReplication(t *testing.T, tablets []vitesst.TabletInfo, value string) {
	t.Helper()

	ctx := t.Context()
	query := fmt.Sprintf("SELECT count(*) FROM balancer_test WHERE value = '%s'", value)

	require.Eventually(t, func() bool {
		for _, tablet := range tablets {
			res, err := tablet.QueryTablet(ctx, query)
			if err != nil || len(res.Rows) == 0 {
				return false
			}

			if val, err := res.Rows[0][0].ToUint64(); err != nil || val != 1 {
				return false
			}
		}

		return true
	}, 15*time.Second, 500*time.Millisecond)
}
