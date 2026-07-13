package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	params := json.RawMessage(`{"ttl":"30m"}`)
	req := Request{V: ProtocolVersion, ID: "r1", Method: "lease.acquire", Params: params}
	okResp := Response{V: ProtocolVersion, ID: "r1", OK: true, Result: json.RawMessage(`{"lease":{"id":"L1"}}`)}
	errResp := Response{
		V: ProtocolVersion, ID: "r1", OK: false,
		Error: mustError(t, "CAPACITY_QUEUED", "queued", "simurgh status --queue", 2),
	}

	var buf bytes.Buffer
	for _, msg := range []any{req, okResp, errResp} {
		if err := Encode(&buf, msg); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	first := buf.Bytes()[:bytes.IndexByte(buf.Bytes(), '\n')+1]
	if bytes.Contains(first[:len(first)-1], []byte{'\n'}) || bytes.Contains(first, []byte{' '}) {
		t.Fatalf("first frame must be compact single-line JSON: %q", first)
	}

	dec := NewDecoder(&buf)
	gotReq, err := dec.DecodeRequest()
	if err != nil || gotReq.ID != req.ID || gotReq.Method != req.Method || !bytes.Equal(gotReq.Params, params) {
		t.Fatalf("DecodeRequest = %+v, %v", gotReq, err)
	}
	gotOK, err := dec.DecodeResponse()
	if err != nil || !gotOK.OK || !bytes.Equal(gotOK.Result, okResp.Result) || gotOK.Error != nil {
		t.Fatalf("ok DecodeResponse = %+v, %v", gotOK, err)
	}
	gotErr, err := dec.DecodeResponse()
	if err != nil || gotErr.OK || gotErr.Error == nil || gotErr.Error.Code != "CAPACITY_QUEUED" || deref(gotErr.Error.QueuePosition) != 2 {
		t.Fatalf("err DecodeResponse = %+v, %v", gotErr, err)
	}
	if _, err := dec.DecodeRequest(); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		max     int
		req     bool
		wantIs  error
		wantVer bool
	}{
		{name: "empty", input: "\n", req: true, wantIs: ErrEmptyFrame},
		{name: "whitespace", input: "   \n", req: true, wantIs: ErrEmptyFrame},
		{name: "malformed", input: "{not-json\n", req: true, wantIs: ErrMalformedJSON},
		{name: "oversize_nl", input: strings.Repeat("a", 16) + "\n", max: 16, req: true, wantIs: ErrOversizedFrame},
		{name: "trunc_short", input: `{"v":1`, req: true, wantIs: ErrTruncatedFrame},
		{name: "trunc_at_limit", input: strings.Repeat("b", 16), max: 16, req: true, wantIs: ErrTruncatedFrame},
		{name: "missing_id", input: `{"v":1,"id":"","method":"daemon.ping"}` + "\n", req: true, wantIs: ErrMissingID},
		{name: "missing_method", input: `{"v":1,"id":"r1"}` + "\n", req: true, wantIs: ErrMissingMethod},
		{name: "resp_missing_id", input: `{"v":1,"id":"","ok":true}` + "\n", wantIs: ErrMissingID},
		{name: "resp_no_error", input: `{"v":1,"id":"r1","ok":false}` + "\n", wantIs: ErrInvalidError},
		{name: "resp_bad_triple", input: `{"v":1,"id":"r1","ok":false,"error":{"code":"X","message":"m"}}` + "\n", wantIs: ErrInvalidError},
		{name: "ok_with_error", input: `{"v":1,"id":"r1","ok":true,"error":{"code":"X","message":"m","fix":"simurgh status"}}` + "\n", wantIs: ErrUnexpectedError},
		{name: "err_with_result", input: `{"v":1,"id":"r1","ok":false,"result":{"x":1},"error":{"code":"X","message":"m","fix":"simurgh status"}}` + "\n", wantIs: ErrUnexpectedResult},
		{name: "req_ver", input: `{"v":2,"id":"r1","method":"daemon.ping"}` + "\n", req: true, wantIs: ErrUnsupportedVersion, wantVer: true},
		{name: "resp_ver", input: `{"v":99,"id":"r1","ok":true}` + "\n", wantIs: ErrUnsupportedVersion, wantVer: true},
		{name: "missing_v", input: `{"id":"r1","method":"daemon.ping"}` + "\n", req: true, wantIs: ErrInvalidVersion},
		{name: "zero_v", input: `{"v":0,"id":"r1","ok":true}` + "\n", wantIs: ErrInvalidVersion},
		{name: "neg_v", input: `{"v":-1,"id":"r1","method":"daemon.ping"}` + "\n", req: true, wantIs: ErrInvalidVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec := NewDecoder(strings.NewReader(tt.input))
			if tt.max > 0 {
				dec.maxSize = tt.max
			}
			var err error
			if tt.req {
				_, err = dec.DecodeRequest()
			} else {
				_, err = dec.DecodeResponse()
			}
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("err = %v, want %v", err, tt.wantIs)
			}
			if tt.wantVer {
				var uerr *UnsupportedVersionError
				if !errors.As(err, &uerr) || uerr.ErrorEnvelope.Code != CodeUnsupportedVersion || uerr.ErrorEnvelope.Fix != FixUnsupportedVersion {
					t.Fatalf("UnsupportedVersionError = %#v", err)
				}
				if !strings.Contains(FixUnsupportedVersion, "simurgh doctor") {
					t.Fatalf("FixUnsupportedVersion must reference simurgh doctor: %q", FixUnsupportedVersion)
				}
			}
		})
	}
}

func TestTruncatedFrameBoundaries(t *testing.T) {
	// Under-limit and exact-limit EOF without newline are truncated, not oversized.
	for _, tt := range []struct {
		name  string
		input string
		max   int
	}{
		{name: "short", input: `{"v":1,"id":"r1"`, max: 64},
		{name: "exact", input: strings.Repeat("x", 8), max: 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dec := NewDecoder(strings.NewReader(tt.input))
			dec.maxSize = tt.max
			if _, err := dec.DecodeRequest(); !errors.Is(err, ErrTruncatedFrame) {
				t.Fatalf("err = %v, want ErrTruncatedFrame", err)
			}
		})
	}
	// One byte past max before newline is oversized.
	dec := NewDecoder(strings.NewReader(strings.Repeat("y", 9) + "\n"))
	dec.maxSize = 8
	if _, err := dec.DecodeRequest(); !errors.Is(err, ErrOversizedFrame) {
		t.Fatalf("err = %v, want ErrOversizedFrame", err)
	}
}

func TestMaxFrameSizeIncludesNewline(t *testing.T) {
	payload := []byte(`{"v":1,"id":"r1","method":"daemon.ping"}`)
	maxOK := len(payload) + 1
	maxBad := len(payload)

	req := Request{V: ProtocolVersion, ID: "r1", Method: "daemon.ping"}
	if err := encode(io.Discard, req, maxOK); err != nil {
		t.Fatalf("encode at exact max: %v", err)
	}
	if err := encode(io.Discard, req, maxBad); !errors.Is(err, ErrOversizedFrame) {
		t.Fatalf("encode without newline budget: %v", err)
	}

	frame := append(append([]byte{}, payload...), '\n')
	decOK := NewDecoder(bytes.NewReader(frame))
	decOK.maxSize = maxOK
	if _, err := decOK.DecodeRequest(); err != nil {
		t.Fatalf("decode at exact max: %v", err)
	}
	decBad := NewDecoder(bytes.NewReader(frame))
	decBad.maxSize = maxBad
	if _, err := decBad.DecodeRequest(); !errors.Is(err, ErrOversizedFrame) {
		t.Fatalf("decode without newline budget: %v", err)
	}
}

func TestNoProgressReader(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := NewDecoder(stallReader{}).DecodeRequest()
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoProgress) {
			t.Fatalf("err = %v, want ErrNoProgress", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DecodeRequest spun forever on (0,nil) reader")
	}
}

func TestNewErrorAndEncodeRejects(t *testing.T) {
	if _, err := NewError("", "m", "simurgh status"); !errors.Is(err, ErrInvalidError) {
		t.Fatalf("NewError: %v", err)
	}
	got, err := NewErrorWithQueuePosition("X", "m", "simurgh status", 3)
	if err != nil || deref(got.QueuePosition) != 3 {
		t.Fatalf("NewErrorWithQueuePosition: %+v, %v", got, err)
	}
	for _, tt := range []struct {
		msg    any
		wantIs error
	}{
		{Request{V: ProtocolVersion, ID: "r1"}, ErrMissingMethod},
		{Response{V: 2, ID: "r1", OK: true}, ErrUnsupportedVersion},
		{Response{V: 0, ID: "r1", OK: true}, ErrInvalidVersion},
		{Request{V: -3, ID: "r1", Method: "daemon.ping"}, ErrInvalidVersion},
		{Response{V: ProtocolVersion, ID: "r1", OK: false, Error: &Error{Code: "X", Message: "m"}}, ErrInvalidError},
		{Response{V: ProtocolVersion, ID: "r1", OK: true, Error: &Error{Code: "X", Message: "m", Fix: "simurgh status"}}, ErrUnexpectedError},
		{Response{V: ProtocolVersion, ID: "r1", OK: false, Result: json.RawMessage(`{}`), Error: &Error{Code: "X", Message: "m", Fix: "simurgh status"}}, ErrUnexpectedResult},
	} {
		if err := Encode(io.Discard, tt.msg); !errors.Is(err, tt.wantIs) {
			t.Fatalf("Encode(%v) = %v, want %v", tt.msg, err, tt.wantIs)
		}
	}
}

func mustError(t *testing.T, code, message, fix string, queue int) *Error {
	t.Helper()
	e, err := NewErrorWithQueuePosition(code, message, fix, queue)
	if err != nil {
		t.Fatalf("mustError: %v", err)
	}
	return e
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// stallReader always returns (0, nil), violating the io.Reader contract.
type stallReader struct{}

func (stallReader) Read(p []byte) (int, error) {
	return 0, nil
}
