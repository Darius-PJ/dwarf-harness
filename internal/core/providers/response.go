package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"strings"
)

// ResponseEvidence contains bounded provider data, not a settlement decision.
// The caller must apply its pinned-secret capture policy before publishing it.
type ResponseEvidence struct {
	Status     int
	Headers    []ResponseHeader
	Stream     StreamResult
	ErrorClass string
	ErrorBody  json.RawMessage
}
type ResponseHeader struct {
	Name   string
	Values []string
}

func responseHeaders(h http.Header) ([]ResponseHeader, error) {
	var out []ResponseHeader
	size := 0
	for name, values := range h {
		name = strings.ToLower(name)
		keep := slices.Contains([]string{"request-id", "anthropic-organization-id", "anthropic-workspace-id", "retry-after", "content-type"}, name) || strings.HasPrefix(name, "anthropic-ratelimit-") || strings.HasPrefix(name, "anthropic-priority-")
		for _, value := range values {
			size += len(name) + len(value) + 4
			if size > 64<<10 {
				return nil, ErrLimit
			}
		}
		if keep {
			out = append(out, ResponseHeader{name, slices.Clone(values)})
		}
	}
	slices.SortFunc(out, func(a, b ResponseHeader) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ReadAnthropicResponse consumes but does not close the response body. HTTP
// errors, refusal, EOF and network errors never imply a free request or retry.
func ReadAnthropicResponse(response *http.Response, p Profile, c Compiled) (ResponseEvidence, error) {
	var result ResponseEvidence
	if response == nil || response.Body == nil {
		return result, ErrInvalid
	}
	result.Status = response.StatusCode
	headers, err := responseHeaders(response.Header)
	if err != nil {
		return result, err
	}
	result.Headers = headers
	encoding := response.Header.Values("Content-Encoding")
	if len(encoding) > 1 || len(encoding) == 1 && encoding[0] != "" && encoding[0] != "identity" {
		result.ErrorClass = "protocol_error"
		return result, ErrProtocol
	}
	if response.StatusCode == http.StatusOK {
		media, params, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if e != nil || media != "text/event-stream" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
			result.ErrorClass = "protocol_error"
			return result, ErrProtocol
		}
		result.Stream, err = ReadAnthropicStream(response.Body, p, c)
		if err != nil {
			result.ErrorClass = "protocol_error"
			if len(result.Stream.ProviderError) > 0 {
				result.ErrorClass = classifyError(200, result.Stream.ProviderError)
			}
		} else if result.Stream.StopReason == "refusal" {
			result.ErrorClass = "refused"
		}
		return result, err
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		result.ErrorClass = ClassifyTransportError(err)
		return result, ErrProtocol
	}
	if len(b) > 64<<10 {
		return result, ErrLimit
	}
	result.ErrorClass = classifyError(response.StatusCode, b)
	if err = checkJSON(b, 64<<10, false, false); err != nil {
		return result, ErrProtocol
	}
	if _, err = rawObject(b); err != nil {
		return result, err
	}
	result.ErrorBody = bytes.Clone(b)
	return result, nil
}

func classifyError(status int, raw []byte) string {
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Details struct {
				Code string `json:"error_code"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	e := body.Error
	switch {
	case status == 401 || status == 403:
		return "denied"
	case status == 402 || status == 429 && e.Details.Code == "enforced_spend_limit_reached" || status == 400 && e.Type == "invalid_request_error" && (strings.HasPrefix(e.Message, "You have reached your specified API usage limits") || strings.HasPrefix(e.Message, "You have reached your specified workspace API usage limits")):
		return "exhausted"
	case status == 429:
		return "rate_limited"
	case status == 529 || e.Type == "overloaded_error":
		return "overloaded"
	case status == 400 || status == 413:
		return "invalid_request"
	case status == 404:
		return "unavailable"
	case status == 409:
		return "conflict"
	case status == 504:
		return "timeout"
	case status >= 500:
		return "provider_error"
	default:
		return "protocol_error"
	}
}

func ClassifyTransportError(err error) string {
	if err == nil {
		return ""
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	return "unreachable"
}
