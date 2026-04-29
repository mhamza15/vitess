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

	t.Cleanup(TearDownCluster)

	_, err = replica1.VttabletProcess.QueryTablet("select 1", keyspaceName, false)
	require.NoError(t, err)

	installSlowShutdownHook(t, filepath.Join(replica1.VttabletProcess.Directory, "mysql.sock"))

	output, err := localCluster.VtctldClientProcess.ExecuteCommandWithOutput(
		"Backup",
		replica1.Alias,
	)
	require.Error(t, err)

	require.Contains(t, output+err.Error(), "mysqld_shutdown hook failed")

	require.Eventually(t, func() bool {
		_, err := replica1.VttabletProcess.QueryTablet("select 1", keyspaceName, false)
		return err == nil
	}, 30*time.Second, time.Second, "mysqld on tablet %s did not restart", replica1.Alias)
}

// installSlowShutdownHook creates a shutdown hook that stops MySQL successfully
// but does not return before the builtin backup shutdown context expires.
func installSlowShutdownHook(t *testing.T, socketPath string) {
	t.Helper()

	hookPath := filepath.Join(os.Getenv("VTROOT"), "vthook", "mysqld_shutdown")

	hookContents := fmt.Sprintf(`#!/bin/sh

mysqladmin --protocol=socket --socket=%q --user=vt_dba --password=%q shutdown
sleep 30
`, socketPath, dbPassword)

	require.NoError(t, os.WriteFile(hookPath, []byte(hookContents), 0o755))
	t.Cleanup(func() {
		require.NoError(t, os.Remove(hookPath))
	})
}
