package trail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/klauspost/compress/zstd"
	"github.com/lithammer/shortuuid/v4"
)

var (
	// enc is a global request encoder.
	enc *zstd.Encoder

	// dec is a global request decoder.
	dec *zstd.Decoder
)

func init() {
	enc, _ = zstd.NewWriter(nil)
	dec, _ = zstd.NewReader(nil)
}

// Request represents a request with trail trace support enabled
type Request struct {
	origin    *http.Request
	response  *httpSpanWriter
	requestId string
	userAgent string
	status    int
	version   string
	method    string
	host      string
	url       *url.URL
	ip        net.IP
	referrer  string
	root      *Span

	userId     string
	profile    []byte
	location   *Location
	operations []Span
	groups     map[string]struct{}
}

// Location of the origin request
type Location struct {
	CountryCode     string  `json:"countryCode,omitempty"`
	CountryCode3    string  `json:"countryCode3,omitempty"`
	CountryName     string  `json:"countryName,omitempty"`
	CityName        string  `json:"cityName,omitempty"`
	Latitude        float64 `json:"latitude,omitempty"`
	Longitude       float64 `json:"longitude,omitempty"`
	TimeZone        string  `json:"timeZone,omitempty"`
	ContinentCode   string  `json:"continentCode,omitempty"`
	SubdivisionCode string  `json:"subdivisionCode,omitempty"`
}

// SetProfile sets a custom profile for the request
func (r *Request) SetProfile(profile interface{}) {
	if b, err := json.Marshal(profile); err == nil {
		r.profile = b
	}
}

// Profile decodes the profile into the value
func (r *Request) Profile(v interface{}) error {
	return json.Unmarshal(r.profile, v)
}

// Context gets original the request context
func (r *Request) Context() context.Context {
	return r.root.ctx
}

// SetStatus sets a custom response status code
func (r *Request) SetStatus(status int) {
	r.status = status
}

// SetUserId sets a custom user for the request
func (r *Request) SetUserId(userId string) {
	r.userId = userId
}

// UserId gets the user id
func (r *Request) UserId() string {
	return r.userId
}

// AddResponseHeaders decodes the trail request from a response header
func (r *Request) AddResponseHeaders(headers http.Header) {
	if header := headers.Get("Request-Trail"); header != "" {
		var data serializedRequest
		b, _ := base64.StdEncoding.DecodeString(header)
		b, _ = dec.DecodeAll(b, nil)
		_ = json.Unmarshal(b, &data)
		r.operations = append(r.operations, data.Operations...)
		if len(r.profile) == 0 && len(data.Profile) > 0 {
			r.profile = data.Profile
		}

		for group, _ := range data.Groups {
			r.AddGroups(group)
		}

		if r.location == nil && data.Location != nil {
			r.SetLocation(data.Location)
		}

		if r.userId == "" && data.UserId != "" {
			r.SetUserId(data.UserId)
		}
	}
}

// Finish ends the current request and sends a response
func (r *Request) Finish() {
	r.root.Finish()
	for {
		select {
		case op := <-r.root.bundle.spans:
			if !op.EndTime.IsZero() {
				r.operations = append(r.operations, *op)
			}
		default:
			return
		}
	}
}

// Recover from a panic
func (r *Request) Recover(v interface{}) {
	r.root.Recover(v)
}

// RequestId gets the request id
func (r *Request) RequestId() string {
	return r.requestId
}

// UserAgent gets the user agent
func (r *Request) UserAgent() string {
	return r.userAgent
}

// Method gets the request method
func (r *Request) Method() string {
	return r.method
}

// Status gets the response status code
func (r *Request) Status() int {
	return r.status
}

// Version gets the version of the request
func (r *Request) Version() string {
	return r.version
}

// URL gets the original url of the request
func (r *Request) URL() *url.URL {
	return r.url
}

// IP gets the ip of the request
func (r *Request) IP() net.IP {
	return r.ip
}

// SetLocation sets a custom location of the request
func (r *Request) SetLocation(location *Location) {
	r.location = location
}

// Location gets the request location
func (r *Request) Location() *Location {
	return r.location
}

// Operations gets the request operations
func (r *Request) Operations() []Span {
	return r.operations
}

// Duration gets the duration of the request
func (r *Request) Duration() time.Duration {
	return r.root.EndTime.Sub(r.root.StartTime)
}

// AddGroups adds custom group information to the request
func (r *Request) AddGroups(groupIds ...string) {
	if r.groups == nil {
		r.groups = make(map[string]struct{})
	}

	for _, groupId := range groupIds {
		r.groups[groupId] = struct{}{}
	}
}

// Groups gets the groups of the request
func (r *Request) Groups() []string {
	var groups []string
	for group, _ := range r.groups {
		groups = append(groups, group)
	}
	return groups
}

// Response gets the underlying response writer
func (r *Request) Response(withTrailHeader bool) http.ResponseWriter {
	r.response.withTrailHeader = withTrailHeader
	return r.response
}

// Origin gets the origin http request
func (r *Request) Origin() *http.Request {
	return r.origin
}

// Referrer gets the referrer of the request
func (r *Request) Referrer() string {
	return r.referrer
}

// Trail gets the encoded request trail
func (r *Request) Trail() string {
	var uri string
	if r.url != nil {
		uri = r.url.String()
	}

	b, _ := json.Marshal(serializedRequest{
		RequestId:  r.requestId,
		UserId:     r.userId,
		Status:     r.status,
		UserAgent:  r.userAgent,
		Version:    r.version,
		URL:        uri,
		Method:     r.method,
		IP:         r.ip,
		Location:   r.location,
		Groups:     r.groups,
		Operations: r.operations,
		StartTime:  r.root.StartTime,
		EndTime:    r.root.EndTime,
		Profile:    r.profile,
		Root:       r.root,
		Referrer:   r.referrer,
	})

	var trail string
	if len(b) > 0 {
		trail = base64.StdEncoding.EncodeToString(enc.EncodeAll(b, make([]byte, 0, len(b))))
	}
	return trail
}

// NewRequest creates a new trail request instance (or continues from a prev one)
func NewRequest(w http.ResponseWriter, r *http.Request, version string) (*Request, error) {
	ctx := r.Context()
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub().Clone()
		ctx = sentry.SetHubOnContext(ctx, hub)
	}

	r = r.WithContext(ctx)
	span := StartSpan(r.Context(), fmt.Sprintf("%s %s/%s", r.Method, r.Host, strings.TrimPrefix(r.URL.Path, "/")))
	var req Request
	if header := r.Header.Get("Request-Trail"); header != "" {
		var data serializedRequest
		b, err := base64.StdEncoding.DecodeString(header)
		if err != nil {
			return nil, Stacktrace(err)
		}

		b, err = dec.DecodeAll(b, nil)
		if err != nil {
			return nil, Stacktrace(err)
		}

		if err := json.Unmarshal(b, &data); err != nil {
			return nil, Stacktrace(err)
		}

		req = data.Request()
	} else {
		req = Request{
			requestId: shortuuid.NewWithNamespace(version),
			userAgent: r.UserAgent(),
			url:       r.URL,
			method:    r.Method,
			ip:        net.ParseIP(r.Header.Get("X-Forwarded-For")),
			version:   version,
			referrer:  r.Header.Get("Referrer"),
		}
	}

	req.origin = r.WithContext(span.Context())
	req.root = span
	span.SetRequest(&req)
	req.response = &httpSpanWriter{w: w, r: &req}
	return &req, nil
}

type serializedRequest struct {
	RequestId  string              `json:"requestId"`
	UserAgent  string              `json:"userAgent"`
	UserId     string              `json:"userId,omitempty"`
	Status     int                 `json:"status,omitempty"`
	Version    string              `json:"version,omitempty"`
	Method     string              `json:"method,omitempty"`
	Host       string              `json:"host,omitempty"`
	URL        string              `json:"url,omitempty"`
	IP         net.IP              `json:"ip,omitempty"`
	Profile    []byte              `json:"profile,omitempty"`
	Location   *Location           `json:"location,omitempty"`
	Groups     map[string]struct{} `json:"groups,omitempty"`
	Operations []Span              `json:"operations,omitempty"`
	StartTime  time.Time           `json:"startTime"`
	EndTime    time.Time           `json:"endTime"`
	Root       *Span               `json:"root,omitempty"`
	Referrer   string              `json:"referrer,omitempty"`
}

// Request gets a trail request from a serialized one
func (h serializedRequest) Request() Request {
	r := Request{
		requestId:  h.RequestId,
		userId:     h.UserId,
		status:     h.Status,
		userAgent:  h.UserAgent,
		version:    h.Version,
		ip:         h.IP,
		method:     h.Method,
		location:   h.Location,
		operations: h.Operations,
		groups:     h.Groups,
		profile:    h.Profile,
		root:       h.Root,
		referrer:   h.Referrer,
	}

	r.url, _ = url.Parse(h.URL)
	return r
}
