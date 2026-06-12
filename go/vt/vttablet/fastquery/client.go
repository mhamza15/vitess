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

	"vitess.io/vitess/go/sqltypes"
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

// roundtrip writes one request frame and reads the response frame. When
// reuseReadBuf is false the payload is read into a fresh buffer that
// the caller may alias beyond this call.
func (fc *Conn) roundtrip(method byte, req vtMarshaler, reuseReadBuf bool) (byte, []byte, error) {
	out, err := appendFrame(fc.writeBuf, method, req)
	if err != nil {
		return 0, nil, err
	}
	fc.writeBuf = out
	if _, err := fc.c.Write(out); err != nil {
		return 0, nil, err
	}
	var readBuf []byte
	if reuseReadBuf {
		readBuf = fc.readBuf
	}
	tag, payload, err := readFrame(fc.r, readBuf)
	if err != nil {
		return 0, nil, err
	}
	if reuseReadBuf {
		fc.readBuf = payload
	}
	return tag, payload, nil
}

// appError decodes a statusAppError payload.
func appError(payload []byte) (appErr error, ok bool) {
	rpcErr := &vtrpcpb.RPCError{}
	if err := rpcErr.UnmarshalVT(payload); err != nil {
		return err, false
	}
	return tabletconn.ErrorFromVTRPC(rpcErr), true
}

// call performs one request/response round trip, unmarshalling a
// statusOK payload into resp. On transport errors the connection is no
// longer usable and ok is false.
func (fc *Conn) call(method byte, req vtMarshaler, resp vtUnmarshaler) (appErr error, ok bool) {
	tag, payload, err := fc.roundtrip(method, req, true)
	if err != nil {
		return err, false
	}
	switch tag {
	case statusOK:
		if err := resp.UnmarshalVT(payload); err != nil {
			return err, false
		}
		return nil, true
	case statusAppError:
		return appError(payload)
	default:
		return status.Error(codes.Internal, "fastquery: unknown response status"), false
	}
}

// callResult performs an Execute round trip, decoding the custom result
// payload. The result aliases a per-call buffer, so the connection's
// read buffer is not reused.
func (fc *Conn) callResult(req vtMarshaler) (result *sqltypes.Result, appErr error, ok bool) {
	tag, payload, err := fc.roundtrip(MethodExecute, req, false)
	if err != nil {
		return nil, err, false
	}
	switch tag {
	case statusResult:
		result, err := decodeResult(payload)
		if err != nil {
			return nil, err, false
		}
		return result, nil, true
	case statusAppError:
		appErr, ok := appError(payload)
		return nil, appErr, ok
	default:
		return nil, status.Error(codes.Internal, "fastquery: unknown response status"), false
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

// do runs fn on a pooled connection, handling context cancellation,
// pool management and transport-error translation. The error is
// translated exactly like a unary grpc call error. The returned bool is
// false when the transport is unavailable (dial failure) and the caller
// should fall back to grpc permanently.
func (p *Pool) do(ctx context.Context, fn func(fc *Conn) (error, bool)) (error, bool) {
	fc, err := p.get()
	if err != nil {
		return err, false
	}

	// Tear down the connection if the caller's context fires mid-call:
	// closing unblocks the pending read.
	stop := context.AfterFunc(ctx, fc.close)

	appErr, connOK := fn(fc)

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

// Call performs one round trip using a pooled connection, decoding a
// success payload into resp.
func (p *Pool) Call(ctx context.Context, method byte, req vtMarshaler, resp vtUnmarshaler) (error, bool) {
	return p.do(ctx, func(fc *Conn) (error, bool) {
		return fc.call(method, req, resp)
	})
}

// Execute performs one Execute round trip using a pooled connection,
// returning the decoded result directly.
func (p *Pool) Execute(ctx context.Context, req *querypb.ExecuteRequest) (*sqltypes.Result, error, bool) {
	var result *sqltypes.Result
	err, ok := p.do(ctx, func(fc *Conn) (error, bool) {
		var appErr error
		var connOK bool
		result, appErr, connOK = fc.callResult(req)
		return appErr, connOK
	})
	if err != nil {
		return nil, err, ok
	}
	return result, nil, ok
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
