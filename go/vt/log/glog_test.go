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

package log

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang/glog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlogOutput(t *testing.T) {
	logDir := t.TempDir()
	require.NoError(t, flag.Set("log_dir", logDir))
	require.NoError(t, flag.Set("logtostderr", "false"))
	require.NoError(t, flag.Set("alsologtostderr", "false"))

	logger := newGlogLogger()
	logger.Info().Str("tablet_id", "zone1-001").Int("shard", 42).Msg("processing tablet")
	glog.Flush()

	matches, err := filepath.Glob(filepath.Join(logDir, "*.INFO.*"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Contains(t, string(content), "processing tablet tablet_id=zone1-001 shard=42")
}
