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
	"bufio"
	"context"
	"log/slog"
	"net"

	"vitess.io/vitess/go/sqltypes"
	"vitess.io/vitess/go/vt/callerid"
	"vitess.io/vitess/go/vt/log"
	"vitess.io/vitess/go/vt/vterrors"
	"vitess.io/vitess/go/vt/vttablet/queryservice"

	querypb "vitess.io/vitess/go/vt/proto/query"
)

// Serve accepts fastquery connections on l and serves queries against
// qs until the listener is closed.
func Serve(l net.Listener, qs queryservice.QueryService) {
	for {
		c, err := l.Accept()
		if err != nil {
			log.Info("fastquery: listener closed", slog.Any("error", err))
			return
		}
		go serveConn(c, qs)
	}
}

func serveConn(c net.Conn, qs queryservice.QueryService) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	r := bufio.NewReaderSize(c, 64<<10)
	var readBuf, writeBuf []byte
	for {
		tag, payload, err := readFrame(r, readBuf)
		if err != nil {
			// Connection closed or protocol error: drop the conn.
			return
		}
		readBuf = payload

		var response vtMarshaler
		var appErr error
		switch tag {
		case MethodExecute:
			request := &querypb.ExecuteRequest{}
			if err := request.UnmarshalVT(payload); err != nil {
				log.Warn("fastquery: failed to unmarshal request, closing connection", slog.Any("error", err))
				return
			}
			response, appErr = execute(context.Background(), qs, request)
		case MethodBeginExecute:
			request := &querypb.BeginExecuteRequest{}
			if err := request.UnmarshalVT(payload); err != nil {
				log.Warn("fastquery: failed to unmarshal request, closing connection", slog.Any("error", err))
				return
			}
			response, appErr = beginExecute(context.Background(), qs, request)
		case MethodCommit:
			request := &querypb.CommitRequest{}
			if err := request.UnmarshalVT(payload); err != nil {
				log.Warn("fastquery: failed to unmarshal request, closing connection", slog.Any("error", err))
				return
			}
			response, appErr = commit(context.Background(), qs, request)
		default:
			log.Warn("fastquery: unknown method tag, closing connection", slog.Int("tag", int(tag)))
			return
		}

		var out []byte
		if appErr != nil {
			out, err = appendFrame(writeBuf, statusAppError, vterrors.ToVTRPC(appErr))
		} else {
			out, err = appendFrame(writeBuf, statusOK, response)
		}
		if err != nil {
			log.Warn("fastquery: failed to marshal response, closing connection", slog.Any("error", err))
			return
		}
		writeBuf = out

		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func beginExecute(ctx context.Context, qs queryservice.QueryService, request *querypb.BeginExecuteRequest) (response *querypb.BeginExecuteResponse, err error) {
	defer qs.HandlePanic(&err)
	ctx = callerid.NewContext(ctx,
		request.EffectiveCallerId,
		request.ImmediateCallerId,
	)
	state, result, err := qs.BeginExecute(ctx, nil, request.Target, request.PreQueries, request.Query.Sql, request.Query.BindVariables, request.ReservedId, request.Options)
	if err != nil {
		// If we have a valid transactionID, return the error in-band.
		if state.TransactionID != 0 {
			return &querypb.BeginExecuteResponse{
				Error:         vterrors.ToVTRPC(err),
				TransactionId: state.TransactionID,
				TabletAlias:   state.TabletAlias,
			}, nil
		}
		return nil, err
	}
	return &querypb.BeginExecuteResponse{
		Result:              sqltypes.ResultToProto3(result),
		TransactionId:       state.TransactionID,
		TabletAlias:         state.TabletAlias,
		SessionStateChanges: state.SessionStateChanges,
	}, nil
}

func commit(ctx context.Context, qs queryservice.QueryService, request *querypb.CommitRequest) (response *querypb.CommitResponse, err error) {
	defer qs.HandlePanic(&err)
	ctx = callerid.NewContext(ctx,
		request.EffectiveCallerId,
		request.ImmediateCallerId,
	)
	rID, err := qs.Commit(ctx, request.Target, request.TransactionId)
	if err != nil {
		return nil, err
	}
	return &querypb.CommitResponse{ReservedId: rID}, nil
}

func execute(ctx context.Context, qs queryservice.QueryService, request *querypb.ExecuteRequest) (response *querypb.ExecuteResponse, err error) {
	defer qs.HandlePanic(&err)
	ctx = callerid.NewContext(ctx,
		request.EffectiveCallerId,
		request.ImmediateCallerId,
	)
	result, err := qs.Execute(ctx, nil, request.Target, request.Query.Sql, request.Query.BindVariables, request.TransactionId, request.ReservedId, request.Options)
	if err != nil {
		return nil, err
	}
	return &querypb.ExecuteResponse{
		Result: sqltypes.ResultToProto3(result),
	}, nil
}
