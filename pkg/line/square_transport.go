package line

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	thrift "github.com/apache/thrift/lib/go/thrift"
)

// SquareClient is the isolated transport used for LINE Square APIs. It wraps
// an existing Talk Client so HTTP policy, credentials, and session lifecycle
// remain owned by the normal LINE session.
type SquareClient struct {
	client *Client
	seq    atomic.Int32
}

// SquareReadOperation identifies one explicitly registered Square read.
// Operations without a verified endpoint and schema are rejected.
type SquareReadOperation string

var ErrSquareOperationNotAllowed = errors.New("square read operation is not allowlisted")

type squareOperationSpec struct {
	host              string
	path              string
	method            string
	validateArguments func([]SquareField) error
}

// Keep this registry empty until a host, exact path, method, and argument
// schema are confirmed from protocol evidence. This makes the initial
// transport deny all authenticated network calls by default.
var verifiedSquareReadOperations = map[SquareReadOperation]squareOperationSpec{}

// SquareField represents a field in a compact-Thrift struct. Initial Square
// read operations require scalar, binary, and nested struct values.
type SquareField struct {
	ID    int16
	Value any
}

// SquareStruct represents a nested compact-Thrift struct.
type SquareStruct struct {
	Fields []SquareField
}

// SquareReadRequest is the minimum typed request envelope for an explicit
// Square read call. Callers supply the already-audited method and arguments;
// this package does not guess endpoints or operation fields.
type SquareReadRequest struct {
	Method     string
	SequenceID int32
	Arguments  []SquareField
}

// SquareReadResponse preserves the Thrift method, sequence, and opaque result
// fields without assuming a service-exception field layout.
type SquareReadResponse struct {
	Method     string
	SequenceID int32
	Fields     []SquareField
}

// NewSquareClient creates an isolated Square transport. It never loads or
// persists access tokens; the parent Client is the existing session lifecycle.
func NewSquareClient(client *Client) *SquareClient {
	return &SquareClient{client: client}
}

// SquareClient provides the isolated transport for the existing session API.
// It exists as an optional bridge; no independent credential source is used.
func (c *Client) SquareClient() *SquareClient {
	return NewSquareClient(c)
}

// NewSquareRequest creates a read request with a deterministic sequence when
// sequenceID is zero. A caller may supply a persisted sequence explicitly.
func (c *SquareClient) NewSquareRequest(method string, sequenceID int32, arguments []SquareField) SquareReadRequest {
	if sequenceID == 0 {
		sequenceID = c.seq.Add(1)
	}
	return SquareReadRequest{Method: method, SequenceID: sequenceID, Arguments: arguments}
}

// EncodeSquareRequest encodes a Square request call using Compact Protocol.
func EncodeSquareRequest(request SquareReadRequest) ([]byte, error) {
	if request.Method == "" {
		return nil, errors.New("square method must not be empty")
	}
	buffer := thrift.NewTMemoryBuffer()
	protocol := thrift.NewTCompactProtocolConf(buffer, nil)
	ctx := context.Background()
	if err := protocol.WriteMessageBegin(ctx, request.Method, thrift.CALL, request.SequenceID); err != nil {
		return nil, fmt.Errorf("write Square message: %w", err)
	}
	if err := protocol.WriteStructBegin(ctx, request.Method+"_args"); err != nil {
		return nil, fmt.Errorf("write Square arguments: %w", err)
	}
	for _, field := range request.Arguments {
		if err := writeSquareField(ctx, protocol, field); err != nil {
			return nil, err
		}
	}
	if err := protocol.WriteFieldStop(ctx); err != nil {
		return nil, fmt.Errorf("write Square arguments end: %w", err)
	}
	if err := protocol.WriteStructEnd(ctx); err != nil {
		return nil, fmt.Errorf("write Square arguments struct end: %w", err)
	}
	if err := protocol.WriteMessageEnd(ctx); err != nil {
		return nil, fmt.Errorf("finish Square request: %w", err)
	}
	return append([]byte(nil), buffer.Bytes()...), nil
}

func writeSquareField(ctx context.Context, protocol thrift.TProtocol, field SquareField) error {
	typeID, err := squareFieldType(field.Value)
	if err != nil {
		return fmt.Errorf("square field %d: %w", field.ID, err)
	}
	if err := protocol.WriteFieldBegin(ctx, "", typeID, field.ID); err != nil {
		return fmt.Errorf("write Square field %d: %w", field.ID, err)
	}
	switch value := field.Value.(type) {
	case bool:
		err = protocol.WriteBool(ctx, value)
	case int8:
		err = protocol.WriteByte(ctx, value)
	case int16:
		err = protocol.WriteI16(ctx, value)
	case int32:
		err = protocol.WriteI32(ctx, value)
	case int:
		err = protocol.WriteI32(ctx, int32(value))
	case int64:
		err = protocol.WriteI64(ctx, value)
	case float64:
		err = protocol.WriteDouble(ctx, value)
	case string:
		err = protocol.WriteString(ctx, value)
	case []byte:
		err = protocol.WriteBinary(ctx, value)
	case SquareStruct:
		err = writeSquareStruct(ctx, protocol, value)
	case *SquareStruct:
		err = writeSquareStruct(ctx, protocol, *value)
	default:
		err = fmt.Errorf("unsupported Thrift value type %T", value)
	}
	if err != nil {
		return fmt.Errorf("write Square field %d value: %w", field.ID, err)
	}
	return protocol.WriteFieldEnd(ctx)
}

func writeSquareStruct(ctx context.Context, protocol thrift.TProtocol, value SquareStruct) error {
	if err := protocol.WriteStructBegin(ctx, ""); err != nil {
		return err
	}
	for _, field := range value.Fields {
		if err := writeSquareField(ctx, protocol, field); err != nil {
			return err
		}
	}
	if err := protocol.WriteFieldStop(ctx); err != nil {
		return err
	}
	return protocol.WriteStructEnd(ctx)
}

func squareFieldType(value any) (thrift.TType, error) {
	switch value.(type) {
	case bool:
		return thrift.BOOL, nil
	case int8:
		return thrift.BYTE, nil
	case int16:
		return thrift.I16, nil
	case int32, int:
		return thrift.I32, nil
	case int64:
		return thrift.I64, nil
	case float64:
		return thrift.DOUBLE, nil
	case string:
		return thrift.STRING, nil
	case []byte:
		return thrift.STRING, nil
	case SquareStruct, *SquareStruct:
		return thrift.STRUCT, nil
	default:
		return thrift.STOP, fmt.Errorf("unsupported Thrift value type %T", value)
	}
}

// DecodeSquareResponse decodes a Compact-Thrift reply or Thrift application
// exception without interpreting any result fields as a login or mutation.
func DecodeSquareResponse(ctx context.Context, data []byte) (*SquareReadResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("empty Square Thrift response")
	}
	buffer := thrift.NewTMemoryBuffer()
	buffer.Write(data)
	protocol := thrift.NewTCompactProtocolConf(buffer, nil)
	name, messageType, sequenceID, err := protocol.ReadMessageBegin(ctx)
	if err != nil {
		return nil, fmt.Errorf("decode Square message: %w", err)
	}
	response := &SquareReadResponse{Method: name, SequenceID: sequenceID}
	switch messageType {
	case thrift.EXCEPTION:
		exception := thrift.NewTApplicationException(thrift.UNKNOWN_APPLICATION_EXCEPTION, "")
		if err := exception.Read(ctx, protocol); err != nil {
			return nil, fmt.Errorf("decode Square application exception: %w", err)
		}
		if err := protocol.ReadMessageEnd(ctx); err != nil {
			return nil, fmt.Errorf("finish Square application exception: %w", err)
		}
		return nil, &SquareApplicationError{ApplicationException: exception}
	case thrift.REPLY:
		if _, err := protocol.ReadStructBegin(ctx); err != nil {
			return nil, fmt.Errorf("decode Square reply struct: %w", err)
		}
		for {
			_, typeID, fieldID, err := protocol.ReadFieldBegin(ctx)
			if err != nil {
				return nil, fmt.Errorf("decode Square reply field: %w", err)
			}
			if typeID == thrift.STOP {
				break
			}
			field, err := readSquareField(ctx, protocol, fieldID, typeID)
			if err != nil {
				return nil, fmt.Errorf("decode Square reply field %d: %w", fieldID, err)
			}
			response.Fields = append(response.Fields, field)
			if err := protocol.ReadFieldEnd(ctx); err != nil {
				return nil, fmt.Errorf("finish Square reply field %d: %w", fieldID, err)
			}
		}
		if err := protocol.ReadStructEnd(ctx); err != nil {
			return nil, fmt.Errorf("finish Square reply struct: %w", err)
		}
		if err := protocol.ReadMessageEnd(ctx); err != nil {
			return nil, fmt.Errorf("finish Square reply: %w", err)
		}
		return response, nil
	default:
		return nil, fmt.Errorf("unexpected Square Thrift message type %d", messageType)
	}
}

func readSquareField(ctx context.Context, protocol thrift.TProtocol, fieldID int16, typeID thrift.TType) (SquareField, error) {
	switch typeID {
	case thrift.BOOL:
		value, err := protocol.ReadBool(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.BYTE:
		value, err := protocol.ReadByte(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.I16:
		value, err := protocol.ReadI16(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.I32:
		value, err := protocol.ReadI32(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.I64:
		value, err := protocol.ReadI64(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.DOUBLE:
		value, err := protocol.ReadDouble(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.STRING:
		value, err := protocol.ReadString(ctx)
		return SquareField{ID: fieldID, Value: value}, err
	case thrift.STRUCT:
		value, err := readSquareStruct(ctx, protocol)
		return SquareField{ID: fieldID, Value: value}, err
	default:
		if err := thrift.SkipDefaultDepth(ctx, protocol, typeID); err != nil {
			return SquareField{}, err
		}
		return SquareField{ID: fieldID}, nil
	}
}

func readSquareStruct(ctx context.Context, protocol thrift.TProtocol) (SquareStruct, error) {
	value := SquareStruct{}
	if _, err := protocol.ReadStructBegin(ctx); err != nil {
		return value, err
	}
	for {
		_, typeID, fieldID, err := protocol.ReadFieldBegin(ctx)
		if err != nil {
			return value, err
		}
		if typeID == thrift.STOP {
			break
		}
		field, err := readSquareField(ctx, protocol, fieldID, typeID)
		if err != nil {
			return value, err
		}
		value.Fields = append(value.Fields, field)
		if err := protocol.ReadFieldEnd(ctx); err != nil {
			return value, err
		}
	}
	if err := protocol.ReadStructEnd(ctx); err != nil {
		return value, err
	}
	return value, nil
}

// SquareApplicationError translates Thrift application exceptions into a typed
// error compatible with the existing session error wrappers.
type SquareApplicationError struct {
	ApplicationException thrift.TApplicationException
}

func (e *SquareApplicationError) Error() string {
	if e.ApplicationException == nil {
		return "LINE Square Thrift application exception"
	}
	return fmt.Sprintf("LINE Square Thrift application exception (type %d)", e.ApplicationException.TypeId())
}

func (e *SquareApplicationError) Unwrap() error {
	if e.ApplicationException == nil {
		return nil
	}
	return e.ApplicationException
}

// CallRead performs one registered read RPC. Callers cannot select a URL or
// method; both are resolved from the verified operation registry.
func (c *SquareClient) CallRead(ctx context.Context, operation SquareReadOperation, arguments []SquareField) (*SquareReadResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("square transport has no LINE client")
	}
	spec, ok := verifiedSquareReadOperations[operation]
	if !ok {
		return nil, ErrSquareOperationNotAllowed
	}
	return c.callReadWithSpec(ctx, spec, arguments)
}

func (c *SquareClient) callReadWithSpec(ctx context.Context, spec squareOperationSpec, arguments []SquareField) (*SquareReadResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("square transport has no LINE client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.method == "" || spec.validateArguments == nil {
		return nil, ErrSquareOperationNotAllowed
	}
	if err := spec.validateArguments(arguments); err != nil {
		return nil, fmt.Errorf("invalid Square read arguments: %w", err)
	}
	fullURL, err := squareEndpoint(spec.host, spec.path)
	if err != nil {
		return nil, fmt.Errorf("invalid Square allowlist entry: %w", err)
	}
	request := c.NewSquareRequest(spec.method, 0, arguments)
	body, err := EncodeSquareRequest(request)
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Square request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/x-thrift")
	httpRequest.Header.Set("User-Agent", UserAgent)
	httpRequest.Header.Set("x-line-chrome-version", ExtensionVersion)
	httpRequest.Header.Set("x-line-application", lineApplicationHeader)
	httpRequest.Header.Set("x-lal", "en_US")
	if c.client.AccessToken != "" {
		httpRequest.Header.Set("x-line-access", c.client.AccessToken)
		httpRequest.Header.Set("Cookie", fmt.Sprintf("lct=%s", c.client.AccessToken))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := doLINERequest(c.client.HTTPClient, httpRequest)
	if err != nil {
		return nil, requestFailure("transport", err)
	}
	c.client.recordClock(response.Header)
	responseBody, err := readResponseBody(response, maxRPCResponseBytes)
	if err != nil {
		return nil, requestFailure("response_read", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &ResponseError{Status: response.StatusCode, body: string(responseBody)}
	}
	decoded, err := DecodeSquareResponse(ctx, responseBody)
	if err != nil {
		return nil, err
	}
	if decoded.Method != request.Method || decoded.SequenceID != request.SequenceID {
		return nil, errors.New("square Thrift response does not match request")
	}
	return decoded, nil
}

func squareEndpoint(host, path string) (string, error) {
	if host == "" || path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "?#") || strings.Contains(path, "\\") {
		return "", errors.New("host and exact absolute path are required")
	}
	parsed, err := url.Parse("https://" + host + path)
	if err != nil || parsed.Scheme != "https" || parsed.Host != host || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != path {
		return "", errors.New("host/path is not a valid exact HTTPS endpoint")
	}
	return parsed.String(), nil
}
