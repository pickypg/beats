// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package http_endpoint

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/testing/testutils"
	"github.com/elastic/elastic-agent-libs/logp"
	"github.com/elastic/elastic-agent-libs/mapstr"
)

var withTraces = flag.Bool("log-traces", false, "specify logging request traces during tests")

const traceLogsDir = "trace_logs"

func Test_httpReadJSON(t *testing.T) {
	log := logp.NewLogger("http_endpoint_test")

	tests := []struct {
		name       string
		body       string
		program    string
		wantObjs   []mapstr.M
		wantStatus int
		wantErr    bool
	}{
		{
			name:       "single object",
			body:       `{"a": 42, "b": "c"}`,
			wantObjs:   []mapstr.M{{"a": int64(42), "b": "c"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "array accepted",
			body:       `[{"a":"b"},{"c":"d"}]`,
			wantObjs:   []mapstr.M{{"a": "b"}, {"c": "d"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "not an object not accepted",
			body:       `42`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name: "not an object mixed",
			body: `[{a:1},
								42,
							{a:2}]`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "sequence of objects accepted (CRLF)",
			body:       `{"a":1}` + "\r" + `{"a":2}`,
			wantObjs:   []mapstr.M{{"a": int64(1)}, {"a": int64(2)}},
			wantStatus: http.StatusOK,
		},
		{
			name: "sequence of objects accepted (LF)",
			body: `{"a":"1"}
									{"a":"2"}`,
			wantObjs:   []mapstr.M{{"a": "1"}, {"a": "2"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "sequence of objects accepted (SP)",
			body:       `{"a":"2"} {"a":"2"}`,
			wantObjs:   []mapstr.M{{"a": "2"}, {"a": "2"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "sequence of objects accepted (no separator)",
			body:       `{"a":"2"}{"a":"2"}`,
			wantObjs:   []mapstr.M{{"a": "2"}, {"a": "2"}},
			wantStatus: http.StatusOK,
		},
		{
			name: "not an object in sequence",
			body: `{"a":"2"}
									42
						 {"a":"2"}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "array of objects in stream",
			body:       `{"a":"1"} [{"a":"2"},{"a":"3"}] {"a":"4"}`,
			wantObjs:   []mapstr.M{{"a": "1"}, {"a": "2"}, {"a": "3"}, {"a": "4"}},
			wantStatus: http.StatusOK,
		},
		{
			name: "numbers",
			body: `{"a":1} [{"a":false},{"a":3.14}] {"a":-4}`,
			wantObjs: []mapstr.M{
				{"a": int64(1)},
				{"a": false},
				{"a": 3.14},
				{"a": int64(-4)},
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "kinesis",
			body: `{
  "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f",
  "timestamp": 1578090901599,
  "records": [
    {
      "data": "aGVsbG8=",
      "number": 1
    },
    {
      "data": "c21hbGwgd29ybGQ=",
      "number": 9007199254740991
    },
    {
      "data": "aGVsbG8gd29ybGQ=",
      "number": 9007199254740992
    },
    {
      "data": "YmlnIHdvcmxk",
      "number": 9223372036854775808
    },
    {
      "data": "d2lsbCBpdCBiZSBmcmllbmRzIHdpdGggbWU=",
      "number": 3.14
    }
  ]
}`,
			program: `obj.records.map(r, {
				"requestId": debug("REQID", obj.requestId),
				"timestamp": string(obj.timestamp), // leave timestamp in unix milli for ingest to handle.
				"event": r,
			})`,
			wantObjs: []mapstr.M{
				{"event": map[string]any{"data": "aGVsbG8=", "number": int64(1)}, "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f", "timestamp": "1578090901599"},
				{"event": map[string]any{"data": "c21hbGwgd29ybGQ=", "number": int64(9007199254740991)}, "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f", "timestamp": "1578090901599"},
				{"event": map[string]any{"data": "aGVsbG8gd29ybGQ=", "number": "9007199254740992"}, "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f", "timestamp": "1578090901599"},
				{"event": map[string]any{"data": "YmlnIHdvcmxk", "number": "9223372036854775808"}, "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f", "timestamp": "1578090901599"},
				{"event": map[string]any{"data": "d2lsbCBpdCBiZSBmcmllbmRzIHdpdGggbWU=", "number": 3.14}, "requestId": "ed4acda5-034f-9f42-bba1-f29aea6d7d8f", "timestamp": "1578090901599"},
			},
			wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := newProgram(tt.program, log)
			if err != nil {
				t.Fatalf("failed to compile program: %v", err)
			}
			gotObjs, gotStatus, err := httpReadJSON(strings.NewReader(tt.body), prg)
			if (err != nil) != tt.wantErr {
				t.Errorf("httpReadJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !assert.EqualValues(t, tt.wantObjs, gotObjs) {
				t.Errorf("httpReadJSON() gotObjs = %v, want %v", gotObjs, tt.wantObjs)
			}
			if gotStatus != tt.wantStatus {
				t.Errorf("httpReadJSON() gotStatus = %v, want %v", gotStatus, tt.wantStatus)
			}
		})
	}
}

type publisher struct {
	mu     sync.Mutex
	events []beat.Event
}

func (p *publisher) Publish(e beat.Event) {
	p.mu.Lock()
	p.events = append(p.events, e)
	if ack, ok := e.Private.(*batchACKTracker); ok {
		ack.ACK()
	}
	p.mu.Unlock()
}

func Test_apiResponse(t *testing.T) {
	if *withTraces {
		err := os.RemoveAll(traceLogsDir)
		if err != nil && errors.Is(err, fs.ErrExist) {
			t.Fatalf("failed to remove trace logs directory: %v", err)
		}
		err = os.Mkdir(traceLogsDir, 0o750)
		if err != nil {
			t.Fatalf("failed to make trace logs directory: %v", err)
		}
	}
	testCases := []struct {
		name         string             // Sub-test name.
		setup        func(t *testing.T) // setup function
		conf         config             // Load configuration.
		request      *http.Request      // Input request.
		events       []mapstr.M         // Expected output events.
		wantStatus   int                // Expected response code.
		wantResponse string             // Expected response message.
	}{
		{
			name: "single_event",
			conf: defaultConfig(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events: []mapstr.M{
				{
					"json": mapstr.M{
						"id": int64(0),
					},
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name: "single_event_root",
			conf: func() config {
				c := defaultConfig()
				c.Prefix = "."
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events: []mapstr.M{
				{
					"id": int64(0),
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name: "options_with_headers",
			conf: func() config {
				c := defaultConfig()
				c.OptionsHeaders = http.Header{
					"optional-response-header": {"Optional-response-value"},
				}
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodOptions, "/", nil)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       []mapstr.M{},
			wantStatus:   http.StatusOK,
			wantResponse: "",
		},
		{
			name: "options_empty_headers",
			conf: func() config {
				c := defaultConfig()
				c.OptionsHeaders = http.Header{}
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodOptions, "/", nil)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       []mapstr.M{},
			wantStatus:   http.StatusOK,
			wantResponse: "",
		},
		{
			name: "options_no_header",
			conf: defaultConfig(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodOptions, "/", nil)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       []mapstr.M{},
			wantStatus:   http.StatusBadRequest,
			wantResponse: `{"message":"OPTIONS requests are only allowed with options_headers set"}`,
		},
		{
			name:  "hmac_hex",
			setup: func(t *testing.T) { testutils.SkipIfFIPSOnly(t, "test HMAC uses SHA-1.") },
			conf: func() config {
				c := defaultConfig()
				c.Prefix = "."
				c.HMACHeader = "Test-HMAC"
				c.HMACKey = "Test-HMAC-Key"
				c.HMACType = "sha1"
				c.HMACPrefix = "sha1:"
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Test-HMAC", "sha1:f6bf232bf1f0ca3d768f8b6bd5c26a204ba57e89")
				return req
			}(),
			events: []mapstr.M{
				{
					"id": int64(0),
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name:  "hmac_base64",
			setup: func(t *testing.T) { testutils.SkipIfFIPSOnly(t, "test HMAC uses SHA-1.") },
			conf: func() config {
				c := defaultConfig()
				c.Prefix = "."
				c.HMACHeader = "Test-HMAC"
				c.HMACKey = "Test-HMAC-Key"
				c.HMACType = "sha1"
				c.HMACPrefix = "sha1:"
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Test-HMAC", "sha1:9r8jK/Hwyj12j4tr1cJqIEulfok=")
				return req
			}(),
			events: []mapstr.M{
				{
					"id": int64(0),
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name:  "hmac_raw_base64",
			setup: func(t *testing.T) { testutils.SkipIfFIPSOnly(t, "test HMAC uses SHA-1.") },
			conf: func() config {
				c := defaultConfig()
				c.Prefix = "."
				c.HMACHeader = "Test-HMAC"
				c.HMACKey = "Test-HMAC-Key"
				c.HMACType = "sha1"
				c.HMACPrefix = "sha1:"
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Test-HMAC", "sha1:9r8jK/Hwyj12j4tr1cJqIEulfok")
				return req
			}(),
			events: []mapstr.M{
				{
					"id": int64(0),
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name: "hmac_header_not_present",
			conf: func() config {
				c := defaultConfig()
				c.HMACHeader = "Authorization"
				c.HMACKey = "mysecretkey"
				c.HMACType = "sha256"
				c.HMACPrefix = "HMAC-SHA256 "
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			wantStatus:   http.StatusUnauthorized,
			wantResponse: `{"message":"missing HMAC header"}`,
		},
		{
			name: "hmac_header_value_is_empty",
			conf: func() config {
				c := defaultConfig()
				c.HMACHeader = "Authorization"
				c.HMACKey = "mysecretkey"
				c.HMACType = "sha256"
				c.HMACPrefix = "HMAC-SHA256 "
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "")
				return req
			}(),
			wantStatus:   http.StatusUnauthorized,
			wantResponse: `{"message":"invalid HMAC signature encoding: unexpected empty header value"}`,
		},
		{
			name: "hmac_header_value_only_contains_prefix",
			conf: func() config {
				c := defaultConfig()
				c.HMACHeader = "Authorization"
				c.HMACKey = "mysecretkey"
				c.HMACType = "sha256"
				c.HMACPrefix = "HMAC-SHA256 "
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "HMAC-SHA256 ")
				return req
			}(),
			wantStatus:   http.StatusUnauthorized,
			wantResponse: `{"message":"invalid HMAC signature encoding: unexpected empty header value"}`,
		},
		{
			name: "hmac_header_value_bad_encoding",
			conf: func() config {
				c := defaultConfig()
				c.HMACHeader = "Authorization"
				c.HMACKey = "mysecretkey"
				c.HMACType = "sha256"
				c.HMACPrefix = "HMAC-SHA256 "
				return c
			}(),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":0}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "HMAC-SHA256 not-hex-or-base64")
				return req
			}(),
			wantStatus:   http.StatusUnauthorized,
			wantResponse: `{"message":"invalid HMAC signature encoding: encoding/hex: invalid byte: U+006E 'n'\nillegal base64 data at input byte 3\nillegal base64 data at input byte 3"}`,
		},
		{
			name: "single_event_gzip",
			conf: defaultConfig(),
			request: func() *http.Request {
				buf := new(bytes.Buffer)
				b := gzip.NewWriter(buf)
				_, _ = io.WriteString(b, `{"id":0}`)
				b.Close()

				req := httptest.NewRequest(http.MethodPost, "/", buf)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Content-Encoding", "gzip")
				return req
			}(),
			events: []mapstr.M{
				{
					"json": mapstr.M{
						"id": int64(0),
					},
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name: "multiple_events_gzip",
			conf: defaultConfig(),
			request: func() *http.Request {
				events := []string{
					`{"id":0}`,
					`{"id":1}`,
				}

				buf := new(bytes.Buffer)
				b := gzip.NewWriter(buf)
				_, _ = io.WriteString(b, strings.Join(events, "\n"))
				b.Close()

				req := httptest.NewRequest(http.MethodPost, "/", buf)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Content-Encoding", "gzip")
				return req
			}(),
			events: []mapstr.M{
				{
					"json": mapstr.M{
						"id": int64(0),
					},
				},
				{
					"json": mapstr.M{
						"id": int64(1),
					},
				},
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"message": "success"}`,
		},
		{
			name: "validate_CRC_request",
			conf: config{
				CRCProvider: "Zoom",
				CRCSecret:   "secretValueTest",
			},
			request: func() *http.Request {
				buf := bytes.NewBufferString(
					`{
						"event_ts":1654503849680,
						"event":"endpoint.url_validation",
						"payload": {
							"plainToken":"qgg8vlvZRS6UYooatFL8Aw"
						}
					}`,
				)
				req := httptest.NewRequest(http.MethodPost, "/", buf)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       nil,
			wantStatus:   http.StatusOK,
			wantResponse: `{"encryptedToken":"70c1f2e2e6ca2d39297490d1f9142c7d701415ea8e6151f6562a08fa657a40ff","plainToken":"qgg8vlvZRS6UYooatFL8Aw"}`,
		},
		{
			name: "malformed_CRC_request",
			conf: config{
				CRCProvider: "Zoom",
				CRCSecret:   "secretValueTest",
			},
			request: func() *http.Request {
				buf := bytes.NewBufferString(
					`{
						"event_ts":1654503849680,
						"event":"endpoint.url_validation",
						"payload": {
							"plainToken":"qgg8vlvZRS6UYooatFL8Aw
						}
					}`,
				)
				req := httptest.NewRequest(http.MethodPost, "/", buf)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       nil,
			wantStatus:   http.StatusBadRequest,
			wantResponse: `{"message":"malformed JSON object at stream position 0: invalid character '\\n' in string literal"}`,
		},
		{
			name: "empty_CRC_challenge",
			conf: config{
				CRCProvider: "Zoom",
				CRCSecret:   "secretValueTest",
			},
			request: func() *http.Request {
				buf := bytes.NewBufferString(
					`{
						"event_ts":1654503849680,
						"event":"endpoint.url_validation",
						"payload": {
							"plainToken":""
						}
					}`,
				)
				req := httptest.NewRequest(http.MethodPost, "/", buf)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			events:       nil,
			wantStatus:   http.StatusBadRequest,
			wantResponse: `{"message":"failed decoding \"payload.plainToken\" from CRC request"}`,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			if tc.setup != nil {
				tc.setup(t)
			}
			pub := new(publisher)
			metrics := newInputMetrics("")
			defer metrics.Close()
			apiHandler := newHandler(ctx, newTracerConfig(tc.name, tc.conf, *withTraces), nil, pub.Publish, nil, logp.NewLogger("http_endpoint.test"), metrics)

			// Execute handler.
			respRec := httptest.NewRecorder()
			apiHandler.ServeHTTP(respRec, tc.request)

			// Validate responses.
			assert.Equal(t, tc.wantStatus, respRec.Code)
			assert.Equal(t, tc.wantResponse, strings.TrimSuffix(respRec.Body.String(), "\n"))
			require.Len(t, pub.events, len(tc.events))

			for i, evt := range pub.events {
				assert.EqualValues(t, tc.events[i], evt.Fields)
			}
		})
	}
}

func newTracerConfig(name string, cfg config, withTrace bool) config {
	if !withTrace {
		return cfg
	}
	cfg.Tracer = &tracerConfig{Logger: lumberjack.Logger{
		Filename: filepath.Join(traceLogsDir, name+".ndjson"),
	}}
	return cfg
}
