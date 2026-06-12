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
	"net"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"vitess.io/vitess/go/vt/vttablet/tabletconn"

	querypb "vitess.io/vitess/go/vt/proto/query"
	vtrpcpb "vitess.io/vitess/go/vt/proto/vtrpc"
)

const dialTimeout = 3 * time.Second

// Conn is a single fastquery client connection. One request is in
// flight at a time; use a Pool for concurrency.
type Conn struct {
	c        net.Conn
	r        *bufio.Reader
	writeBuf []byte
	readBuf  []byte
}

func dial(addr string) (*Conn, error) {
	c, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	return &Conn{c: c, r: bufio.NewReaderSize(c, 64<<10)}, nil
}

func (fc *Conn) close() {
	fc.c.Close()
}

// call performs one request/response round trip, unmarshalling a
// statusOK payload into resp. On transport errors the connection is no
// longer usable and ok is false.
func (fc *Conn) call(method byte, req vtMarshaler, resp vtUnmarshaler) (appErr error, ok bool) {
	out, err := appendFrame(fc.writeBuf, method, req)
	if err != nil {
		return err, false
	}
	fc.writeBuf = out
	if _, err := fc.c.Write(out); err != nil {
		return err, false
	}
	tag, payload, err := readFrame(fc.r, fc.readBuf)
	if err != nil {
		return err, false
	}
	fc.readBuf = payload
	switch tag {
	case statusOK:
		if err := resp.UnmarshalVT(payload); err != nil {
			return err, false
		}
		return nil, true
	case statusAppError:
		rpcErr := &vtrpcpb.RPCError{}
		if err := rpcErr.UnmarshalVT(payload); err != nil {
			return err, false
		}
		return tabletconn.ErrorFromVTRPC(rpcErr), true
	default:
		return status.Error(codes.Internal, "fastquery: unknown response status"), false
	}
}

// Pool is a pool of fastquery connections to one tablet.
type Pool struct {
	addr string

	mu     sync.Mutex
	conns  []*Conn
	closed bool
}

// NewPool creates a pool dialing addr on demand.
func NewPool(addr string) *Pool {
	return &Pool{addr: addr}
}

func (p *Pool) get() (*Conn, error) {
	p.mu.Lock()
	if n := len(p.conns); n > 0 {
		fc := p.conns[n-1]
		p.conns[n-1] = nil
		p.conns = p.conns[:n-1]
		p.mu.Unlock()
		return fc, nil
	}
	p.mu.Unlock()
	return dial(p.addr)
}

func (p *Pool) put(fc *Conn) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		fc.close()
		return
	}
	p.conns = append(p.conns, fc)
	p.mu.Unlock()
}

// Close closes all pooled connections.
func (p *Pool) Close() {
	p.mu.Lock()
	conns := p.conns
	p.conns = nil
	p.closed = true
	p.mu.Unlock()
	for _, fc := range conns {
		fc.close()
	}
}

// Call performs one round trip using a pooled connection, decoding a
// success payload into resp. The error is translated exactly like a
// unary grpc call error. The returned bool is false when the transport
// is unavailable (dial failure) and the caller should fall back to
// grpc permanently.
func (p *Pool) Call(ctx context.Context, method byte, req vtMarshaler, resp vtUnmarshaler) (error, bool) {
	fc, err := p.get()
	if err != nil {
		return err, false
	}

	// Tear down the connection if the caller's context fires mid-call:
	// closing unblocks the pending read.
	stop := context.AfterFunc(ctx, fc.close)

	appErr, connOK := fc.call(method, req, resp)

	reusable := stop() && connOK
	if !connOK {
		fc.close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The caller's context fired and tore down the conn;
			// report the caller's deadline/cancellation instead of
			// the teardown artifact.
			return status.FromContextError(ctxErr).Err(), true
		}
		// Transport failure mid-call: surface as UNAVAILABLE so the
		// gateway retry logic treats it like a broken grpc conn.
		return status.Error(codes.Unavailable, appErr.Error()), true
	}
	if reusable {
		p.put(fc)
	} else {
		fc.close()
	}
	return appErr, true
}

// Execute performs one Execute round trip using a pooled connection.
func (p *Pool) Execute(ctx context.Context, req *querypb.ExecuteRequest) (*querypb.ExecuteResponse, error, bool) {
	resp := &querypb.ExecuteResponse{}
	err, ok := p.Call(ctx, MethodExecute, req, resp)
	if err != nil {
		return nil, err, ok
	}
	return resp, nil, ok
}

// BeginExecute performs one BeginExecute round trip using a pooled
// connection.
func (p *Pool) BeginExecute(ctx context.Context, req *querypb.BeginExecuteRequest) (*querypb.BeginExecuteResponse, error, bool) {
	resp := &querypb.BeginExecuteResponse{}
	err, ok := p.Call(ctx, MethodBeginExecute, req, resp)
	if err != nil {
		return nil, err, ok
	}
	return resp, nil, ok
}

// Commit performs one Commit round trip using a pooled connection.
func (p *Pool) Commit(ctx context.Context, req *querypb.CommitRequest) (*querypb.CommitResponse, error, bool) {
	resp := &querypb.CommitResponse{}
	err, ok := p.Call(ctx, MethodCommit, req, resp)
	if err != nil {
		return nil, err, ok
	}
	return resp, nil, ok
}
