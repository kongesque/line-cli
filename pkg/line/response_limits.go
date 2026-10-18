package line

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
)

const (
	maxRPCResponseBytes   = 16 << 20
	maxOBSMetadataBytes   = 64 << 10
	maxErrorResponseBytes = 4 << 10
	maxMediaResponseBytes = (20 << 20) + 32 // plaintext cap plus attachment HMAC
	maxSSELineBytes       = 1 << 20
	maxSSEEventBytes      = 4 << 20
	maxSSEEventLines      = 4096
)

var ErrResponseTooLarge = errors.New("LINE response exceeds size limit")
var ErrInvalidResponseLimit = errors.New("invalid LINE response size limit")

type ResponseLimitError struct{ Limit int64 }

func (e *ResponseLimitError) Error() string {
	return fmt.Sprintf("LINE response exceeds size limit (%d bytes)", e.Limit)
}
func (e *ResponseLimitError) Unwrap() error { return ErrResponseTooLarge }

// readBounded never returns a partial body on error. Limits apply after HTTP
// transfer decoding and automatic decompression, not to Content-Length.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit == math.MaxInt64 {
		return nil, ErrInvalidResponseLimit
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(data)) > limit {
		return nil, &ResponseLimitError{Limit: limit}
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readResponseBody(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	if limit < 0 || limit == math.MaxInt64 {
		return nil, ErrInvalidResponseLimit
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = min(limit, maxErrorResponseBytes)
	}
	return readBounded(resp.Body, limit)
}

// A bounded line reader avoids ReadBytes' unbounded allocation for a single
// unterminated line. The terminator counts toward the line and event budgets.
func readSSELine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := r.ReadSlice('\n')
		if len(fragment) > maxSSELineBytes-len(line) {
			return nil, &ResponseLimitError{Limit: maxSSELineBytes}
		}
		line = append(line, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}
