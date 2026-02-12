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

package vdiff

import (
	"context"
	"testing"
	"testing/synctest"

	"vitess.io/vitess/go/mysql/collations"
	"vitess.io/vitess/go/sqltypes"

	binlogdatapb "vitess.io/vitess/go/vt/proto/binlogdata"
	querypb "vitess.io/vitess/go/vt/proto/query"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"

	"github.com/stretchr/testify/require"
)

// TestShardStreamerNoLeakOnCancel verifies that canceling the context used by
// the primitive executor does not leak goroutines.
func TestShardStreamerNoLeakOnCancel(t *testing.T) {
	tablet := &topodatapb.Tablet{
		Alias:    &topodatapb.TabletAlias{Cell: "zone1", Uid: 200},
		Keyspace: "test_ks",
		Shard:    "0",
		Type:     topodatapb.TabletType_REPLICA,
	}

	origEnv := vdiffenv
	vdiffenv = &testVDiffEnv{tablets: map[int]*fakeTabletConn{200: {tablet: tablet}}}
	t.Cleanup(func() { vdiffenv = origEnv })

	// Override VStreamRows to send one result set then block until the
	// context is canceled, simulating a long-running stream that gets
	// interrupted.
	vstreamRowsOverride = func(ctx context.Context, _ *binlogdatapb.VStreamRowsRequest, send func(*binlogdatapb.VStreamRowsResponse) error) error {
		response := &binlogdatapb.VStreamRowsResponse{
			Fields: []*querypb.Field{{Name: "c1", Type: querypb.Type_INT64}},
			Gtid:   "MySQL56/test:1-1",
			Rows:   []*querypb.Row{{Lengths: []int64{1}, Values: []byte("1")}},
		}

		// First RecvMsg + send(r) iteration, delivers fields/GTID/rows.
		if err := send(response); err != nil {
			return err
		}
		// Subsequent RecvMsg call. We mimic that it's blocked waiting for next batch, which can happen for a
		// large table for example.
		<-ctx.Done()

		// RecvMsg returns an error after gRPC stream canceled (below).
		return ctx.Err()
	}
	t.Cleanup(func() { vstreamRowsOverride = nil })

	synctest.Test(t, func(t *testing.T) {
		collationEnv := collations.MySQL8()

		td := &tableDiffer{
			wd: &workflowDiffer{
				ct: &controller{
					uuid: "test-uuid",
					done: make(chan struct{}),
					vde:  &Engine{thisTablet: tablet},
				},
				collationEnv: collationEnv,
			},
		}

		ss := &shardStreamer{
			tablet: tablet,
			shard:  "0",
			result: make(chan *sqltypes.Result, 1),
		}

		ctx, cancel := context.WithCancel(context.Background())
		gtidch := make(chan string, 1)

		go td.streamOneShard(ctx, ss, "select c1 from t1", nil, gtidch)

		comparePKs := []compareColInfo{{colIndex: 0, collation: collations.CollationBinaryID, isPK: true, colName: "c1"}}
		prim := newMergeSorter(map[string]*shardStreamer{"0": ss}, comparePKs, collationEnv)
		pe := newPrimitiveExecutor(ctx, prim, "test")

		row, err := pe.next()
		require.NoError(t, err)
		require.NotNil(t, row)

		// Cancel the context, simulating a vdiff stop. If the result channel isn't
		// closed, shardStreamer.StreamExecute will remain blocked and synctest.Wait
		// will panic.
		cancel()
		require.NotPanics(t, synctest.Wait)
	})
}
