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

package grpcqueryservice

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"

	"vitess.io/vitess/go/sqltypes"
	"vitess.io/vitess/go/vt/callerid"
	"vitess.io/vitess/go/vt/callinfo"
	"vitess.io/vitess/go/vt/vterrors"

	querypb "vitess.io/vitess/go/vt/proto/query"
)

// executePipeServiceDesc describes the QueryPipe service: a long-lived
// bidirectional stream that carries one ExecuteRequest/ExecuteResponse
// pair per query. Compared to unary Execute calls, a pipe avoids the
// per-RPC HTTP/2 stream setup (HEADERS frames, HPACK encoding, stream
// accounting) and the associated allocations and scheduler wakeups,
// which dominate per-query latency on the vtgate->vttablet hop.
//
// Exactly one request is in flight per pipe; concurrency is achieved by
// the client opening multiple pipes. Application errors terminate the
// pipe with a full grpc status (preserving error fidelity); the client
// opens a fresh pipe for the next query.
var executePipeServiceDesc = grpc.ServiceDesc{
	ServiceName: "queryservice.QueryPipe",
	HandlerType: (*any)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ExecutePipe",
			Handler:       executePipeHandler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "queryservice.QueryPipe",
}

func executePipeHandler(srv any, stream grpc.ServerStream) error {
	q := srv.(*query)
	for {
		request := &querypb.ExecuteRequest{}
		if err := stream.RecvMsg(request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		response, err := q.executePipeOne(stream.Context(), request)
		if err != nil {
			return vterrors.ToGRPC(err)
		}
		if err := stream.SendMsg(response); err != nil {
			return err
		}
	}
}

func (q *query) executePipeOne(ctx context.Context, request *querypb.ExecuteRequest) (response *querypb.ExecuteResponse, err error) {
	defer q.server.HandlePanic(&err)
	ctx = callerid.NewContext(callinfo.GRPCCallInfo(ctx),
		request.EffectiveCallerId,
		request.ImmediateCallerId,
	)
	result, err := q.server.Execute(ctx, nil, request.Target, request.Query.Sql, request.Query.BindVariables, request.TransactionId, request.ReservedId, request.Options)
	if err != nil {
		return nil, err
	}
	return &querypb.ExecuteResponse{
		Result: sqltypes.ResultToProto3(result),
	}, nil
}
