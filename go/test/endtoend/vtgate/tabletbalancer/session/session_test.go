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

// TestSessionModeBalancer tests the "session" mode routes each session consistently to the same tablet.
func TestSessionModeBalancer(t *testing.T) {
	t.Parallel()

	cluster, _ := setupCluster(t)

	// Create 2 session connections that route to different tablets
	conns := createSessionConnections(t, cluster, 2)
	for conn := range conns {
		defer conn.Close()
	}

	verifyStickiness(t, conns, 20)
}

// TestSessionModeRemoveTablet tests that when a tablet is killed, connections switch to remaining tablets
func TestSessionModeRemoveTablet(t *testing.T) {
	t.Parallel()

	cluster, aliases := setupCluster(t)

	// Create 2 connections to different tablets
	conns := createSessionConnections(t, cluster, 2)
	for conn := range conns {
		defer conn.Close()
	}

	// Get all tablets
	tablets := cluster.Tablets()

	// Find the first replica tablet that one of our connections is using
	var tabletToKill *vitesst.TabletInfo
	var affectedConn *mysql.Conn
	var killedServerID int64

	for _, tablet := range tablets {
		tabletServerID := aliases[tablet.Alias()]

		// Check if any connection is using this tablet
		for conn, connServerID := range conns {
			if connServerID != tabletServerID {
				continue
			}

			// We found a connection that's using this tablet, let's kill this tablet
			tabletToKill = &tablet
			affectedConn = conn
			killedServerID = tabletServerID
			break
		}

		// We found a tablet, no need to check other tablets
		if tabletToKill != nil {
			break
		}
	}

	require.NotNil(t, tabletToKill, "Should find a tablet to kill")

	// Kill the tablet immediately
	err := tabletToKill.Kill(t.Context())
	require.NoError(t, err)

	// Wait for the connection to switch to a new tablet and update the map
	require.Eventually(t, func() bool {
		newServerID := getServerID(t, affectedConn)
		if newServerID != killedServerID {
			conns[affectedConn] = newServerID
			return true
		}

		return false
	}, 10*time.Millisecond, 1*time.Millisecond, "Connection should switch to a different tablet")

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

	// Get all tablets and build alias -> serverID map
	tablets := cluster.Tablets()
	aliases := mapTabletAliasToServerID(t, tablets)

	// Insert test data and wait for replication
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

// createSessionConnections creates `n` connections that route to different tablets.
// Returns a map of mysql.Conn -> serverID.
func createSessionConnections(t *testing.T, cluster *vitesst.Cluster, numConnections int) map[*mysql.Conn]int64 {
	t.Helper()

	conns := make(map[*mysql.Conn]int64)
	seenServerIDs := make(map[int64]bool)

	// Try up to 50 times to get numConnections with different server IDs
	for range 50 {
		conn := cluster.ConnectKeyspace(t, sessionKeyspaceName+"@replica")

		// Get the server ID this connection routes to
		serverID := getServerID(t, conn)

		// If this is a new tablet, keep the connection
		if !seenServerIDs[serverID] {
			seenServerIDs[serverID] = true
			conns[conn] = serverID

			// If we have enough connections, return
			if len(conns) == numConnections {
				return conns
			}

			continue
		}

		// Already seen this tablet, close and try again
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
