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
	"encoding/binary"

	"vitess.io/vitess/go/sqltypes"
	"vitess.io/vitess/go/vt/vterrors"

	querypb "vitess.io/vitess/go/vt/proto/query"
	vtrpcpb "vitess.io/vitess/go/vt/proto/vtrpc"
)

// Custom sqltypes.Result wire codec. Encoding a Result directly (instead
// of converting to querypb.QueryResult and proto-marshalling) avoids the
// two conversion layers on each side of the wire that dominate the
// vtgate allocation profile. The decoder performs exactly three
// allocations regardless of row count (rows, values, fields slices) and
// aliases all cell bytes into the frame payload buffer.
//
// Layout (integers little-endian fixed or uvarint as noted):
//
//	[u64 RowsAffected][u64 InsertID][u8 InsertIDChanged][u16 StatusFlags]
//	[uvarint len][Info bytes][uvarint len][SessionStateChanges bytes]
//	[uvarint nFields] nFields x { [uvarint len][querypb.Field proto] }
//	[uvarint nRows][uvarint totalValues]
//	nRows x { [uvarint nValues] nValues x { [uvarint typ][uvarint len][bytes] } }

// appendResult appends the encoded result to buf.
func appendResult(buf []byte, result *sqltypes.Result) ([]byte, error) {
	buf = binary.LittleEndian.AppendUint64(buf, result.RowsAffected)
	buf = binary.LittleEndian.AppendUint64(buf, result.InsertID)
	if result.InsertIDChanged {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	buf = binary.LittleEndian.AppendUint16(buf, result.StatusFlags)
	buf = binary.AppendUvarint(buf, uint64(len(result.Info)))
	buf = append(buf, result.Info...)
	buf = binary.AppendUvarint(buf, uint64(len(result.SessionStateChanges)))
	buf = append(buf, result.SessionStateChanges...)

	buf = binary.AppendUvarint(buf, uint64(len(result.Fields)))
	for _, f := range result.Fields {
		size := f.SizeVT()
		buf = binary.AppendUvarint(buf, uint64(size))
		n := len(buf)
		buf = append(buf, make([]byte, size)...)
		if _, err := f.MarshalToSizedBufferVT(buf[n:]); err != nil {
			return nil, err
		}
	}

	totalValues := 0
	for _, row := range result.Rows {
		totalValues += len(row)
	}
	buf = binary.AppendUvarint(buf, uint64(len(result.Rows)))
	buf = binary.AppendUvarint(buf, uint64(totalValues))
	for _, row := range result.Rows {
		buf = binary.AppendUvarint(buf, uint64(len(row)))
		for _, v := range row {
			buf = binary.AppendUvarint(buf, uint64(v.Type()))
			raw := v.Raw()
			buf = binary.AppendUvarint(buf, uint64(len(raw)))
			buf = append(buf, raw...)
		}
	}
	return buf, nil
}

func decodeErr() error {
	return vterrors.Errorf(vtrpcpb.Code_INTERNAL, "fastquery: malformed result payload")
}

// decodeResult decodes a result encoded by appendResult. Cell bytes
// alias payload, which therefore must not be reused by the caller.
func decodeResult(payload []byte) (*sqltypes.Result, error) {
	result := &sqltypes.Result{}
	if len(payload) < 19 {
		return nil, decodeErr()
	}
	result.RowsAffected = binary.LittleEndian.Uint64(payload)
	result.InsertID = binary.LittleEndian.Uint64(payload[8:])
	result.InsertIDChanged = payload[16] != 0
	result.StatusFlags = binary.LittleEndian.Uint16(payload[17:])
	payload = payload[19:]

	str := func() ([]byte, bool) {
		size, n := binary.Uvarint(payload)
		if n <= 0 || uint64(len(payload)-n) < size {
			return nil, false
		}
		s := payload[n : n+int(size)]
		payload = payload[n+int(size):]
		return s, true
	}
	uvarint := func() (uint64, bool) {
		v, n := binary.Uvarint(payload)
		if n <= 0 {
			return 0, false
		}
		payload = payload[n:]
		return v, true
	}

	info, ok := str()
	if !ok {
		return nil, decodeErr()
	}
	result.Info = string(info)
	ssc, ok := str()
	if !ok {
		return nil, decodeErr()
	}
	result.SessionStateChanges = string(ssc)

	nFields, ok := uvarint()
	if !ok || nFields > uint64(len(payload)) {
		return nil, decodeErr()
	}
	if nFields > 0 {
		result.Fields = make([]*querypb.Field, nFields)
		for i := range result.Fields {
			fbytes, ok := str()
			if !ok {
				return nil, decodeErr()
			}
			f := &querypb.Field{}
			if err := f.UnmarshalVT(fbytes); err != nil {
				return nil, err
			}
			result.Fields[i] = f
		}
	}

	nRows, ok := uvarint()
	if !ok || nRows > uint64(len(payload)) {
		return nil, decodeErr()
	}
	totalValues, ok := uvarint()
	if !ok || totalValues > uint64(len(payload)) {
		return nil, decodeErr()
	}
	result.Rows = make([]sqltypes.Row, nRows)
	if nRows == 0 {
		return result, nil
	}
	values := make([]sqltypes.Value, totalValues)
	used := 0
	for i := range result.Rows {
		nValues, ok := uvarint()
		if !ok || used+int(nValues) > len(values) {
			return nil, decodeErr()
		}
		row := values[used : used+int(nValues) : used+int(nValues)]
		used += int(nValues)
		for j := range row {
			typ, ok := uvarint()
			if !ok {
				return nil, decodeErr()
			}
			raw, ok := str()
			if !ok {
				return nil, decodeErr()
			}
			row[j] = sqltypes.MakeTrusted(querypb.Type(typ), raw)
		}
		result.Rows[i] = row
	}
	return result, nil
}
