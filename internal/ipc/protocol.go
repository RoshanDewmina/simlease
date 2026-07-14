package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ProtocolVersion is the only envelope major version supported in M0.
const ProtocolVersion = 1

// MaxFrameSize is the maximum size of one frame including the trailing newline.
const MaxFrameSize = 1 << 20

const (
	CodeUnsupportedVersion = "UNSUPPORTED_VERSION"
	// FixUnsupportedVersion is a planned CLI command (Architecture §9).
	FixUnsupportedVersion = "simurgh doctor   # install a compatible simurgh version"
)

var (
	ErrEmptyFrame         = errors.New("ipc: empty frame")
	ErrTruncatedFrame     = errors.New("ipc: truncated frame")
	ErrOversizedFrame     = errors.New("ipc: oversized frame")
	ErrMalformedJSON      = errors.New("ipc: malformed JSON frame")
	ErrMissingID          = errors.New("ipc: missing id")
	ErrMissingMethod      = errors.New("ipc: missing method")
	ErrInvalidVersion     = errors.New("ipc: invalid or missing protocol version")
	ErrInvalidError       = errors.New("ipc: error requires code, message, and fix")
	ErrUnexpectedError    = errors.New("ipc: ok response must not include error")
	ErrUnexpectedResult   = errors.New("ipc: error response must not include result")
	ErrUnsupportedVersion = errors.New("ipc: unsupported protocol version")
	ErrNoProgress         = errors.New("ipc: reader made no progress")
)

// Request is a protocol-v1 client request envelope.
type Request struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a protocol-v1 server response envelope.
type Response struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is the stable user-facing error triple plus optional queue metadata.
type Error struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Fix           string `json:"fix"`
	QueuePosition *int   `json:"queuePosition,omitempty"`
	Reason        string `json:"reason,omitempty"`
	ResourceClass string `json:"resourceClass,omitempty"`
}

// UnsupportedVersionError rejects envelopes whose major version is not ProtocolVersion.
type UnsupportedVersionError struct {
	Got           int
	ErrorEnvelope *Error
}

func (e *UnsupportedVersionError) Error() string {
	if e == nil {
		return ErrUnsupportedVersion.Error()
	}
	return fmt.Sprintf("%s: got %d want %d", ErrUnsupportedVersion.Error(), e.Got, ProtocolVersion)
}

func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }

// NewError constructs a validated error triple.
func NewError(code, message, fix string) (*Error, error) {
	return newError(code, message, fix, nil)
}

// NewErrorWithQueuePosition constructs a validated error triple with queue position.
func NewErrorWithQueuePosition(code, message, fix string, queuePosition int) (*Error, error) {
	pos := queuePosition
	return newError(code, message, fix, &pos)
}

func newError(code, message, fix string, queuePosition *int) (*Error, error) {
	err := &Error{Code: code, Message: message, Fix: fix, QueuePosition: queuePosition}
	if err.validate() != nil {
		return nil, ErrInvalidError
	}
	return err, nil
}

func (e *Error) validate() error {
	if e == nil || e.Code == "" || e.Message == "" || e.Fix == "" {
		return ErrInvalidError
	}
	return nil
}

// Decoder reads newline-delimited frames from a single stream without over-reading
// across calls. Prefer one Decoder per connection.
type Decoder struct {
	br      *bufio.Reader
	maxSize int
}

// NewDecoder returns a Decoder bound to r. If r is already a *bufio.Reader it is reused.
func NewDecoder(r io.Reader) *Decoder {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(&progressReader{r: r})
	}
	return &Decoder{br: br, maxSize: MaxFrameSize}
}

// Encode writes exactly one compact JSON object plus a trailing newline.
// The JSON payload plus newline must not exceed MaxFrameSize.
func Encode(w io.Writer, msg any) error {
	return encode(w, msg, MaxFrameSize)
}

func encode(w io.Writer, msg any, maxSize int) error {
	switch m := msg.(type) {
	case Request:
		if err := validateRequest(m); err != nil {
			return err
		}
	case *Request:
		if m == nil {
			return fmt.Errorf("ipc: nil request")
		}
		if err := validateRequest(*m); err != nil {
			return err
		}
	case Response:
		if err := validateResponse(m); err != nil {
			return err
		}
	case *Response:
		if m == nil {
			return fmt.Errorf("ipc: nil response")
		}
		if err := validateResponse(*m); err != nil {
			return err
		}
	default:
		return fmt.Errorf("ipc: unsupported message type %T", msg)
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("ipc: encode: %w", err)
	}
	// maxSize counts the trailing newline.
	if len(raw)+1 > maxSize {
		return ErrOversizedFrame
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// DecodeRequest reads one request frame using a one-shot Decoder.
// For multiple frames on one stream, use NewDecoder.
func DecodeRequest(r io.Reader) (Request, error) {
	return NewDecoder(r).DecodeRequest()
}

// DecodeResponse reads one response frame using a one-shot Decoder.
// For multiple frames on one stream, use NewDecoder.
func DecodeResponse(r io.Reader) (Response, error) {
	return NewDecoder(r).DecodeResponse()
}

// DecodeRequest reads and validates the next request frame.
func (d *Decoder) DecodeRequest() (Request, error) {
	raw, err := d.readFrame()
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Request{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	if err := validateRequest(req); err != nil {
		return Request{}, err
	}
	return req, nil
}

// DecodeResponse reads and validates the next response frame.
func (d *Decoder) DecodeResponse() (Response, error) {
	raw, err := d.readFrame()
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	if err := validateResponse(resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (d *Decoder) readFrame() ([]byte, error) {
	if d.maxSize <= 0 {
		return nil, ErrOversizedFrame
	}

	var buf bytes.Buffer
	total := 0 // includes the terminating newline
	for {
		b, err := d.br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if total == 0 {
					return nil, io.EOF
				}
				// Non-empty frame ended without newline at or under maxSize.
				return nil, ErrTruncatedFrame
			}
			return nil, err
		}
		total++
		if total > d.maxSize {
			return nil, ErrOversizedFrame
		}
		if b == '\n' {
			break
		}
		_ = buf.WriteByte(b)
	}

	// Decode intentionally trims surrounding whitespace so padded single-line
	// JSON frames are accepted; Encode still emits compact objects only.
	raw := bytes.TrimSpace(buf.Bytes())
	if len(raw) == 0 {
		return nil, ErrEmptyFrame
	}
	return append([]byte(nil), raw...), nil
}

func validateVersion(v int) error {
	if v <= 0 {
		return ErrInvalidVersion
	}
	if v != ProtocolVersion {
		return unsupportedVersion(v)
	}
	return nil
}

func validateRequest(req Request) error {
	if err := validateVersion(req.V); err != nil {
		return err
	}
	if strings.TrimSpace(req.ID) == "" {
		return ErrMissingID
	}
	if strings.TrimSpace(req.Method) == "" {
		return ErrMissingMethod
	}
	return nil
}

func validateResponse(resp Response) error {
	if err := validateVersion(resp.V); err != nil {
		return err
	}
	if strings.TrimSpace(resp.ID) == "" {
		return ErrMissingID
	}
	if resp.OK {
		if resp.Error != nil {
			return ErrUnexpectedError
		}
		return nil
	}
	if len(resp.Result) > 0 {
		return ErrUnexpectedResult
	}
	if err := resp.Error.validate(); err != nil {
		return ErrInvalidError
	}
	return nil
}

func unsupportedVersion(got int) error {
	msg := fmt.Sprintf("protocol version %d is unsupported (want %d)", got, ProtocolVersion)
	env, err := NewError(CodeUnsupportedVersion, msg, FixUnsupportedVersion)
	if err != nil {
		return err
	}
	return &UnsupportedVersionError{Got: got, ErrorEnvelope: env}
}

// progressReader rejects io.Reader implementations that return (0, nil) with a
// non-empty buffer, which would otherwise spin forever inside bufio.
type progressReader struct {
	r io.Reader
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n == 0 && err == nil && len(b) > 0 {
		return 0, ErrNoProgress
	}
	return n, err
}
