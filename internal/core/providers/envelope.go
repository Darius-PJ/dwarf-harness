package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Descriptor contains no authentication bytes. The credential identifiers are
// opaque nonsecret identities supplied by the Core owner of the pinned version.
type Descriptor struct {
	Provider          string
	Authority         string
	Model             string
	Region            string
	Method            string
	Path              string
	RequestedTier     string
	CredentialScope   string
	CredentialVersion string
	Headers           []Header
}

type Envelope struct {
	descriptor Descriptor
	body       []byte
	digest     [32]byte
}

func (e Envelope) Descriptor() Descriptor {
	d := e.descriptor
	d.Headers = slices.Clone(d.Headers)
	return d
}
func (e Envelope) Body() []byte     { return bytes.Clone(e.body) }
func (e Envelope) SHA256() [32]byte { return e.digest }

func semanticHeaders(headers []Header) ([]Header, error) {
	if len(headers) != 3 {
		return nil, ErrUnsupported
	}
	out := slices.Clone(headers)
	seen := map[string]bool{}
	allowed := map[string]string{"accept": "text/event-stream", "anthropic-version": APIVersion, "content-type": "application/json"}
	for i, h := range out {
		n := strings.ToLower(h.Name)
		if seen[n] || allowed[n] == "" || h.Value != allowed[n] {
			return nil, ErrUnsupported
		}
		seen[n] = true
		out[i].Name = n
	}
	slices.SortFunc(out, func(a, b Header) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Seal binds only a compiled request for the identical profile. All fields and
// bytes are copied; changing the caller's inputs cannot mutate the envelope.
func Seal(p Profile, c Compiled, scope, version string, headers []Header) (Envelope, error) {
	var e Envelope
	digest, err := p.SHA256()
	if err != nil {
		return e, err
	}
	if digest != c.profile || len(c.body) == 0 || !visible(scope, 256) || !visible(version, 256) {
		return e, ErrInvalid
	}
	hs, err := semanticHeaders(headers)
	if err != nil {
		return e, err
	}
	e.descriptor = Descriptor{p.Provider, p.Authority, p.ModelID, p.Region, p.Method, p.Path, p.RequestedTier, scope, version, hs}
	e.body = bytes.Clone(c.body)
	e.digest = sha256.Sum256(requestPreimage(e.descriptor, e.body))
	return e, nil
}

func frame(b *bytes.Buffer, p []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(p)))
	b.Write(n[:])
	b.Write(p)
}
func frameText(b *bytes.Buffer, s string) { frame(b, []byte(s)) }
func count(b *bytes.Buffer, n int) {
	var u [4]byte
	binary.BigEndian.PutUint32(u[:], uint32(n))
	b.Write(u[:])
}

func requestPreimage(d Descriptor, body []byte) []byte {
	var b bytes.Buffer
	for _, s := range []string{"router-provider-request/v1", d.Provider, d.Authority, d.Model, d.Region, d.Method, d.Path, d.RequestedTier, d.CredentialScope, d.CredentialVersion} {
		frameText(&b, s)
	}
	count(&b, len(d.Headers))
	for _, h := range d.Headers {
		frameText(&b, h.Name)
		frameText(&b, h.Value)
	}
	frame(&b, body)
	return b.Bytes()
}

// RequestSHA256 hashes an already-validated descriptor and its exact recorded
// body. It never recompiles JSON or grants permission to dispatch.
func RequestSHA256(d Descriptor, body []byte) [32]byte {
	return sha256.Sum256(requestPreimage(d, body))
}

// QuoteSHA256 uses raw request hash bytes but lowercase hexadecimal tariff
// digest text. Components are sorted by their complete framed encodings.
func QuoteSHA256(request [32]byte, capability, tariff string, tariffDigest [32]byte, maximum int64, components []Component) ([32]byte, error) {
	var zero [32]byte
	if request == zero || tariffDigest == zero || !visible(capability, 128) || !visible(tariff, 128) {
		return zero, ErrInvalid
	}
	n, err := Maximum(components)
	if err != nil {
		return zero, err
	}
	if maximum != n {
		return zero, ErrInvalid
	}
	var b bytes.Buffer
	frameText(&b, "router-cost-quote/v1")
	frame(&b, request[:])
	frameText(&b, capability)
	frameText(&b, tariff)
	frameText(&b, hex.EncodeToString(tariffDigest[:]))
	frameText(&b, "USD")
	frameText(&b, strconv.FormatInt(maximum, 10))
	encoded := make([][]byte, 0, len(components))
	for _, c := range components {
		var item bytes.Buffer
		classes := slices.Clone(c.Classes)
		slices.Sort(classes)
		count(&item, len(classes))
		for _, cl := range classes {
			frameText(&item, cl)
		}
		for _, s := range []string{strconv.FormatInt(c.MaxUnits, 10), c.Numerator, c.Denominator} {
			frameText(&item, s)
		}
		encoded = append(encoded, item.Bytes())
	}
	slices.SortFunc(encoded, bytes.Compare)
	count(&b, len(encoded))
	for _, item := range encoded {
		frame(&b, item)
	}
	return sha256.Sum256(b.Bytes()), nil
}
