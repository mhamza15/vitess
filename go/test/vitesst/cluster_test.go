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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCluster(t *testing.T) {
	t.Parallel()

	cluster := NewCluster(t,
		WithKeyspace("test_ks").
			WithSchema(`CREATE TABLE t1 (id INT PRIMARY KEY, name VARCHAR(100))`).
			WithVSchema(`{"sharded": false, "tables": {"t1": {}}}`),
	)

	conn := cluster.Connect(t)
	defer conn.Close()

	// Insert
	_, err := conn.ExecuteFetch("INSERT INTO test_ks.t1 (id, name) VALUES (1, 'test')", 1, false)
	require.NoError(t, err)

	// Select
	result, err := conn.ExecuteFetch("SELECT * FROM test_ks.t1 WHERE id = 1", 1, true)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "1", result.Rows[0][0].ToString())
	assert.Equal(t, "test", result.Rows[0][1].ToString())
}
