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

package vtctlbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"vitess.io/vitess/go/test/endtoend/cluster"
	"vitess.io/vitess/go/vt/proto/topodata"
	vtutils "vitess.io/vitess/go/vt/utils"
)

// TestBuiltinBackupShutdownTimeoutRestartsMySQL verifies that a shutdown timeout
// does not leave mysqld stopped after the backup engine has shut it down.
func TestBuiltinBackupShutdownTimeoutRestartsMySQL(t *testing.T) {
	setDefaultCompressionFlag()
	setDefaultCommonArgs()

	defer setDefaultCompressionFlag()
	defer setDefaultCommonArgs()

	commonTabletArg = append(
		commonTabletArg,
		vtutils.GetFlagVariantForTests("--builtinbackup-mysqld-timeout"),
		"3s",
	)

	code, err := LaunchCluster(BuiltinBackup, "xbstream", 0, nil)
	require.NoErrorf(t, err, "setup failed with status code %d", code)

	defer TearDownCluster()

	removeHook := installSlowShutdownHook(t, replica1)
	defer removeHook()

	verifyInitialReplication(t)

	output, err := localCluster.VtctldClientProcess.ExecuteCommandWithOutput(
		"Backup",
		replica1.Alias,
	)
	require.Error(t, err)

	combinedOutput := output + err.Error()
	require.Contains(t, combinedOutput, "can't shutdown mysqld")
	require.Contains(t, combinedOutput, "mysqld_shutdown hook failed")

	waitForTabletMySQL(t, replica1, 30*time.Second)
	checkTabletType(t, replica1.Alias, topodata.TabletType_REPLICA)
}

// installSlowShutdownHook creates a shutdown hook that stops MySQL successfully
// but does not return before the builtin backup shutdown context expires.
func installSlowShutdownHook(t *testing.T, tablet *cluster.Vttablet) func() {
	t.Helper()

	hookPath := filepath.Join(os.Getenv("VTROOT"), "vthook", "mysqld_shutdown")
	socketPath := filepath.Join(tablet.VttabletProcess.Directory, "mysql.sock")

	hookContents := fmt.Sprintf(`#!/bin/sh

mysqladmin --protocol=socket --socket=%q --user=vt_dba --password=%q shutdown
sleep 30
`, socketPath, dbPassword)

	require.NoError(t, os.WriteFile(hookPath, []byte(hookContents), 0o755))

	return func() {
		require.NoError(t, os.Remove(hookPath))
	}
}

// waitForTabletMySQL waits for direct MySQL connections to succeed again.
func waitForTabletMySQL(t *testing.T, tablet *cluster.Vttablet, waitTime time.Duration) {
	t.Helper()

	var lastErr error
	deadline := time.Now().Add(waitTime)

	for time.Now().Before(deadline) {
		_, lastErr = tablet.VttabletProcess.QueryTablet("select 1", keyspaceName, false)
		if lastErr == nil {
			return
		}

		time.Sleep(1 * time.Second)
	}

	require.NoErrorf(t, lastErr, "mysqld on tablet %s did not restart", tablet.Alias)
}
