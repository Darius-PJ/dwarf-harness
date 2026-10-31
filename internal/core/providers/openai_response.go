package providers

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
)

// ReadOpenAIResponse retains only bounded allowlisted evidence and closes the
// body after the terminal, without waiting for EOF or issuing another request.
func ReadOpenAIResponse(response *http.Response, p Profile, c Compiled) (ResponseEvidence, error) {
	return readOpenAIResponse(response, p, c, nil)
}

// ReadOpenAIResponseStreaming emits decoded text through observe while
// independently draining usage. Credential protection belongs to the caller.
func ReadOpenAIResponseStreaming(response *http.Response, p Profile, c Compiled, observe func(string) error) (ResponseEvidence, error) {
	return readOpenAIResponse(response, p, c, observe)
}

func readOpenAIResponse(response *http.Response, p Profile, c Compiled, observe func(string) error) (ResponseEvidence, error) {
	var result ResponseEvidence
	if p.Provider != "openai" {
		return result, ErrUnsupported
	}
	if err := p.Validate(); err != nil {
		return result, err
	}
	digest, _ := p.SHA256()
	if c.profile != digest || response == nil || response.Body == nil {
		return result, ErrInvalid
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	headers, err := responseHeaders(p.Provider, response.Header)
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
		media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || len(response.Header.Values("Content-Type")) != 1 || media != "text/event-stream" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
			result.ErrorClass = "protocol_error"
			return result, ErrProtocol
		}
		result.Stream, err = readOpenAIStream(response.Body, p, c, observe)
		switch {
		case errors.Is(err, ErrTariffAmbiguous):
			result.ErrorClass = "tariff_ambiguous"
			result.Stream.ProviderError = []byte(`{"type":"threshold_ambiguous"}`)
		case len(result.Stream.ProviderError) > 0:
			result.ErrorClass = classifyOpenAIError(200, result.Stream.ProviderError)
		case errors.Is(err, ErrProtocol), errors.Is(err, ErrUsage), errors.Is(err, ErrLimit), errors.Is(err, ErrInvalid):
			result.ErrorClass = "protocol_error"
		case err != nil:
			result.ErrorClass = ClassifyTransportError(err)
		case result.Stream.StopReason == "failed":
			result.ErrorClass = "provider_error"
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
	if checkJSON(b, 64<<10, false, false) != nil {
		result.ErrorClass = "protocol_error"
		return result, ErrProtocol
	}
	if _, err := rawObject(b); err != nil {
		result.ErrorClass = "protocol_error"
		return result, err
	}
	result.ErrorClass = classifyOpenAIError(response.StatusCode, b)
	result.ErrorBody = bytes.Clone(b)
	return result, nil
}

func classifyOpenAIError(status int, raw []byte) string {
	m, _ := rawObject(raw)
	if nested, present := m["error"]; present {
		m, _ = rawObject(nested)
	}
	typ, _ := stringField(m, "type")
	code, _ := stringField(m, "code")
	switch {
	case status == 401 || status == 403:
		return "denied"
	case (status == 429 || status == 200) && (typ == "insufficient_quota" || slices.Contains([]string{"insufficient_quota", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded"}, code)):
		return "exhausted"
	case status == 429 || code == "slow_down" || status == 200 && typ == "rate_limit_error":
		return "rate_limited"
	case (status == 503 || status == 200) && code == "server_is_overloaded":
		return "overloaded"
	case status == 400 || status == 413:
		return "invalid_request"
	case status == 404:
		return "unavailable"
	case status == 409:
		return "conflict"
	case status == 408 || status >= 500 || status == 200 && code == "server_error":
		return "provider_error"
	case typ == "refused":
		return "refused"
	default:
		return "protocol_error"
	}
}
