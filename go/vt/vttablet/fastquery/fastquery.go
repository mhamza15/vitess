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

// Package fastquery implements a minimal TCP transport for the hot
// vtgate->vttablet Execute path. Compared to grpc unary calls (or even
// long-lived grpc streams), it eliminates the HTTP/2 framing machinery
// and, critically, all intermediate goroutine handoffs: the client
// writes the request from the calling goroutine and blocks reading the
// response on its own socket, so the netpoller wakes the caller
// directly. On the server, a dedicated per-connection goroutine reads,
// executes and writes back without handing off.
//
// Wire format, strictly one request in flight per connection:
//
//	request:  [1B method][4B big-endian len][payload proto]
//	response: [1B status][4B big-endian len][payload proto]
//
// method:  1 = Execute (query.ExecuteRequest -> query.ExecuteResponse)
// status:  0 = ok (payload is the response proto)
//
//	1 = application error (payload is vtrpc.RPCError)
//
// Concurrency comes from pooling connections. Transport-level errors
// close the connection; application errors keep it usable.
package fastquery

import (
	"encoding/binary"
	"io"

	querypb "vitess.io/vitess/go/vt/proto/query"
)

const (
	// MethodExecute identifies an Execute request frame.
	MethodExecute = byte(1)

	statusOK       = byte(0)
	statusAppError = byte(1)

	headerSize = 5

	// maxFrameSize bounds a frame payload; matches the default grpc
	// max message size order of magnitude.
	maxFrameSize = 64 << 20
)

type (
	// vtMarshaler is implemented by all vtproto-generated messages.
	vtMarshaler interface {
		SizeVT() int
		MarshalToSizedBufferVT(dAtA []byte) (int, error)
	}

	// vtUnmarshaler is implemented by all vtproto-generated messages.
	vtUnmarshaler interface {
		UnmarshalVT(dAtA []byte) error
	}
)

var _ vtMarshaler = (*querypb.ExecuteRequest)(nil)

// appendFrame appends a [tag][len][payload] frame to buf and returns it.
func appendFrame(buf []byte, tag byte, msg vtMarshaler) ([]byte, error) {
	size := msg.SizeVT()
	need := headerSize + size
	if cap(buf) < need {
		buf = make([]byte, need)
	}
	buf = buf[:need]
	buf[0] = tag
	binary.BigEndian.PutUint32(buf[1:headerSize], uint32(size))
	// MarshalToSizedBufferVT fills the buffer back-to-front.
	if _, err := msg.MarshalToSizedBufferVT(buf[headerSize:]); err != nil {
		return nil, err
	}
	return buf, nil
}

// readFrame reads a [tag][len][payload] frame, reusing buf when possible.
// It returns the tag, the payload and the (possibly grown) buffer.
func readFrame(r io.Reader, buf []byte) (byte, []byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(hdr[1:])
	if size > maxFrameSize {
		return 0, nil, io.ErrUnexpectedEOF
	}
	if cap(buf) < int(size) {
		buf = make([]byte, size)
	}
	buf = buf[:size]
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}
