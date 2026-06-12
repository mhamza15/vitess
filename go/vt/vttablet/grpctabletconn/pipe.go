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

package grpctabletconn

import (
	"context"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	querypb "vitess.io/vitess/go/vt/proto/query"
)

// Execute pipes: long-lived bidirectional grpc streams that carry one
// ExecuteRequest/ExecuteResponse pair per query, avoiding the per-RPC
// HTTP/2 stream setup cost of unary calls. Exactly one request is in
// flight per pipe; a pool of pipes provides concurrency. Any error
// terminates the pipe (the error carries the full grpc status from the
// server); the next call opens a fresh pipe. If the server does not
// implement the pipe service, the client permanently falls back to
// unary Execute calls.

var executePipeStreamDesc = grpc.StreamDesc{
	StreamName:    "ExecutePipe",
	ServerStreams: true,
	ClientStreams: true,
}

const executePipeMethod = "/queryservice.QueryPipe/ExecutePipe"

type executePipe struct {
	stream grpc.ClientStream
	cancel context.CancelFunc
}

func (p *executePipe) discard() {
	p.cancel()
}

type pipePool struct {
	mu       sync.Mutex
	pipes    []*executePipe
	closed   bool
	disabled atomic.Bool // server does not support pipes; use unary
}

func (pp *pipePool) get() *executePipe {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if n := len(pp.pipes); n > 0 {
		p := pp.pipes[n-1]
		pp.pipes[n-1] = nil
		pp.pipes = pp.pipes[:n-1]
		return p
	}
	return nil
}

func (pp *pipePool) put(p *executePipe) {
	pp.mu.Lock()
	if pp.closed {
		pp.mu.Unlock()
		p.discard()
		return
	}
	pp.pipes = append(pp.pipes, p)
	pp.mu.Unlock()
}

func (pp *pipePool) close() {
	pp.mu.Lock()
	pipes := pp.pipes
	pp.pipes = nil
	pp.closed = true
	pp.mu.Unlock()
	for _, p := range pipes {
		p.discard()
	}
}

// executePipe sends an ExecuteRequest over a pooled pipe stream and
// waits for the response. The caller must hold conn.mu.RLock. On any
// error the pipe is discarded; the returned error carries the grpc
// status sent by the server, so it can be translated exactly like a
// unary call error.
func (conn *gRPCQueryClient) executePipe(ctx context.Context, req *querypb.ExecuteRequest) (*querypb.ExecuteResponse, error) {
	if conn.pipes.disabled.Load() {
		return conn.c.Execute(ctx, req)
	}

	p := conn.pipes.get()
	if p == nil {
		// The stream context must outlive this call: the pipe is
		// reused for later queries. Per-call cancellation is enforced
		// via context.AfterFunc below.
		sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		stream, err := conn.cc.NewStream(sctx, &executePipeStreamDesc, executePipeMethod)
		if err != nil {
			cancel()
			return nil, err
		}
		p = &executePipe{stream: stream, cancel: cancel}
	}

	// Cancel the pipe if the caller's context fires mid-call.
	stop := context.AfterFunc(ctx, p.cancel)

	resp := &querypb.ExecuteResponse{}
	err := p.stream.SendMsg(req)
	if err == nil {
		err = p.stream.RecvMsg(resp)
	}

	if err != nil {
		stop()
		p.discard()
		if status.Code(err) == codes.Unimplemented {
			// Server does not support pipes: permanent unary fallback.
			conn.pipes.disabled.Store(true)
			return conn.c.Execute(ctx, req)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The caller's context fired and tore down the pipe;
			// report the caller's deadline/cancellation instead of
			// the stream teardown artifact.
			return nil, status.FromContextError(ctxErr).Err()
		}
		return nil, err
	}

	if !stop() {
		// The context fired after the response was received; the pipe
		// was (or is being) cancelled and cannot be reused.
		p.discard()
		return resp, nil
	}
	conn.pipes.put(p)
	return resp, nil
}
