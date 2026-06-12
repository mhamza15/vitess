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

package fastquery

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vitess.io/vitess/go/sqltypes"
	"vitess.io/vitess/go/vt/vterrors"
	"vitess.io/vitess/go/vt/vttablet/queryservice"

	querypb "vitess.io/vitess/go/vt/proto/query"
	vtrpcpb "vitess.io/vitess/go/vt/proto/vtrpc"
)

type fakeQueryService struct {
	queryservice.QueryService

	execute func(ctx context.Context, sql string) (*sqltypes.Result, error)
}

func (f *fakeQueryService) Execute(ctx context.Context, session queryservice.Session, target *querypb.Target, sql string, bindVariables map[string]*querypb.BindVariable, transactionID, reservedID int64, options *querypb.ExecuteOptions) (*sqltypes.Result, error) {
	return f.execute(ctx, sql)
}

func (f *fakeQueryService) HandlePanic(err *error) {}

func startServer(t *testing.T, qs queryservice.QueryService) *Pool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	go Serve(l, qs)
	pool := NewPool(l.Addr().String())
	t.Cleanup(pool.Close)
	return pool
}

func TestExecuteRoundTrip(t *testing.T) {
	want := sqltypes.MakeTestResult(
		sqltypes.MakeTestFields("id|val", "int64|varchar"),
		"1|abc",
		"2|def",
	)
	pool := startServer(t, &fakeQueryService{
		execute: func(ctx context.Context, sql string) (*sqltypes.Result, error) {
			assert.Equal(t, "select 1", sql)
			return want, nil
		},
	})

	for range 3 {
		resp, appErr, ok := pool.Execute(t.Context(), &querypb.ExecuteRequest{
			Query: &querypb.BoundQuery{Sql: "select 1"},
		})
		require.True(t, ok)
		require.NoError(t, appErr)
		got := sqltypes.Proto3ToResult(resp.Result)
		assert.Equal(t, want.Rows, got.Rows)
	}
}

func TestExecuteAppError(t *testing.T) {
	pool := startServer(t, &fakeQueryService{
		execute: func(ctx context.Context, sql string) (*sqltypes.Result, error) {
			return nil, vterrors.Errorf(vtrpcpb.Code_ALREADY_EXISTS, "duplicate entry")
		},
	})

	_, appErr, ok := pool.Execute(t.Context(), &querypb.ExecuteRequest{
		Query: &querypb.BoundQuery{Sql: "insert"},
	})
	require.True(t, ok)
	require.ErrorContains(t, appErr, "duplicate entry")
	assert.Equal(t, vtrpcpb.Code_ALREADY_EXISTS, vterrors.Code(appErr))

	// The connection must remain usable after an application error.
	_, appErr2, ok2 := pool.Execute(t.Context(), &querypb.ExecuteRequest{
		Query: &querypb.BoundQuery{Sql: "insert"},
	})
	require.True(t, ok2)
	require.ErrorContains(t, appErr2, "duplicate entry")
}

func TestExecuteContextCancel(t *testing.T) {
	block := make(chan struct{})
	pool := startServer(t, &fakeQueryService{
		execute: func(ctx context.Context, sql string) (*sqltypes.Result, error) {
			<-block
			return &sqltypes.Result{}, nil
		},
	})
	t.Cleanup(func() { close(block) })

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, appErr, ok := pool.Execute(ctx, &querypb.ExecuteRequest{
		Query: &querypb.BoundQuery{Sql: "select sleep"},
	})
	require.True(t, ok)
	require.Error(t, appErr)
	assert.ErrorContains(t, appErr, "context canceled")
}

func TestDialFailure(t *testing.T) {
	pool := NewPool("127.0.0.1:1") // nothing listens here
	_, _, ok := pool.Execute(t.Context(), &querypb.ExecuteRequest{
		Query: &querypb.BoundQuery{Sql: "select 1"},
	})
	assert.False(t, ok, "dial failure must report transport unavailable")
}
