package config_test

import (
	"slices"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func TestLoadGRPC_DefaultsOverridesAndInclusiveBounds(t *testing.T) {
	// given: expected values come from the existing scaffold and Product loaders.
	for _, test := range []struct {
		name   string
		values map[string]string
		want   config.GRPC
	}{
		{"absent defaults", nil, config.GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608}},
		{"lower bounds", map[string]string{
			"GRPC_MAX_RECV_BYTES": "1", "GRPC_MAX_SEND_BYTES": "1",
		}, config.GRPC{MaxReceiveBytes: 1, MaxSendBytes: 1}},
		{"upper bounds", map[string]string{
			"GRPC_MAX_RECV_BYTES": "16777216", "GRPC_MAX_SEND_BYTES": "67108864",
		}, config.GRPC{MaxReceiveBytes: 16777216, MaxSendBytes: 67108864}},
		{"explicit overrides", map[string]string{
			"GRPC_MAX_RECV_BYTES": "4096", "GRPC_MAX_SEND_BYTES": "8192",
		}, config.GRPC{MaxReceiveBytes: 4096, MaxSendBytes: 8192}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got, err := config.LoadGRPC(lookup(test.values))
			// then
			if err != nil || got != test.want {
				t.Fatalf(
					"gRPC limits = %+v, error = %v; want %+v",
					got,
					err,
					test.want,
				)
			}
		})
	}
}

func TestLoadGRPC_SafeFirstFailureAndLookupOrder(t *testing.T) {
	for _, test := range []struct {
		name, wantSetting, wantReason string
		values                        map[string]string
		wantLookups                   []string
	}{
		{
			name: "empty receive precedes invalid send", wantSetting: "GRPC_MAX_RECV_BYTES",
			wantReason: "must not be empty",
			values: map[string]string{
				"GRPC_MAX_RECV_BYTES": "", "GRPC_MAX_SEND_BYTES": "secret-marker",
			},
			wantLookups: []string{"GRPC_MAX_RECV_BYTES"},
		},
		{
			name: "receive above maximum", wantSetting: "GRPC_MAX_RECV_BYTES",
			wantReason:  "must be an integer from 1 through 16777216",
			values:      map[string]string{"GRPC_MAX_RECV_BYTES": "16777217"},
			wantLookups: []string{"GRPC_MAX_RECV_BYTES"},
		},
		{
			name: "send above maximum", wantSetting: "GRPC_MAX_SEND_BYTES",
			wantReason:  "must be an integer from 1 through 67108864",
			values:      map[string]string{"GRPC_MAX_SEND_BYTES": "67108865"},
			wantLookups: []string{"GRPC_MAX_RECV_BYTES", "GRPC_MAX_SEND_BYTES"},
		},
		{
			name: "malformed send", wantSetting: "GRPC_MAX_SEND_BYTES",
			wantReason:  "must be an integer from 1 through 67108864",
			values:      map[string]string{"GRPC_MAX_SEND_BYTES": "secret-marker"},
			wantLookups: []string{"GRPC_MAX_RECV_BYTES", "GRPC_MAX_SEND_BYTES"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			var lookups []string
			get := func(name string) (string, bool) {
				lookups = append(lookups, name)
				value, exists := test.values[name]
				return value, exists
			}
			// when
			got, err := config.LoadGRPC(get)
			// then
			assertSafeError(t, err, test.wantSetting)
			setting, reason := config.ErrorDetails(err)
			if setting != test.wantSetting || reason != test.wantReason {
				t.Fatalf(
					"error details = %q, %q; want %q, %q",
					setting,
					reason,
					test.wantSetting,
					test.wantReason,
				)
			}
			if got != (config.GRPC{}) || !slices.Equal(lookups, test.wantLookups) {
				t.Fatalf(
					"gRPC limits = %+v, lookups = %q; want zero and %q",
					got,
					lookups,
					test.wantLookups,
				)
			}
		})
	}
}

func TestLoadHTTPServer_DefaultsAndOverrides(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
		want   config.HTTPServer
	}{
		{"absent defaults", nil, config.HTTPServer{
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		}},
		{"explicit overrides", map[string]string{
			"HTTP_READ_HEADER_TIMEOUT": "750ms", "HTTP_READ_TIMEOUT": "11s",
			"HTTP_WRITE_TIMEOUT": "12s", "HTTP_IDLE_TIMEOUT": "31s",
		}, config.HTTPServer{
			ReadHeaderTimeout: 750 * time.Millisecond, ReadTimeout: 11 * time.Second,
			WriteTimeout: 12 * time.Second, IdleTimeout: 31 * time.Second,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got, err := config.LoadHTTPServer(lookup(test.values))
			// then
			if err != nil || got != test.want {
				t.Fatalf(
					"HTTP timeouts = %+v, error = %v; want %+v",
					got,
					err,
					test.want,
				)
			}
		})
	}
}

func TestLoadHTTPServer_SafeFirstFailureAndLookupOrder(t *testing.T) {
	for _, test := range []struct {
		name, wantSetting, wantReason string
		values                        map[string]string
		wantLookups                   []string
	}{
		{
			name: "empty header precedes invalid idle", wantSetting: "HTTP_READ_HEADER_TIMEOUT",
			wantReason: "must not be empty",
			values: map[string]string{
				"HTTP_READ_HEADER_TIMEOUT": "", "HTTP_IDLE_TIMEOUT": "secret-marker",
			},
			wantLookups: []string{"HTTP_READ_HEADER_TIMEOUT"},
		},
		{
			name: "zero read", wantSetting: "HTTP_READ_TIMEOUT",
			wantReason:  "must be a positive duration",
			values:      map[string]string{"HTTP_READ_TIMEOUT": "0s"},
			wantLookups: []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT"},
		},
		{
			name: "negative write", wantSetting: "HTTP_WRITE_TIMEOUT",
			wantReason: "must be a positive duration",
			values:     map[string]string{"HTTP_WRITE_TIMEOUT": "-1s"},
			wantLookups: []string{
				"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT",
			},
		},
		{
			name: "malformed idle", wantSetting: "HTTP_IDLE_TIMEOUT",
			wantReason: "must be a positive duration",
			values:     map[string]string{"HTTP_IDLE_TIMEOUT": "secret-marker"},
			wantLookups: []string{
				"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT",
				"HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			var lookups []string
			get := func(name string) (string, bool) {
				lookups = append(lookups, name)
				value, exists := test.values[name]
				return value, exists
			}
			// when
			got, err := config.LoadHTTPServer(get)
			// then
			assertSafeError(t, err, test.wantSetting)
			setting, reason := config.ErrorDetails(err)
			if setting != test.wantSetting || reason != test.wantReason {
				t.Fatalf(
					"error details = %q, %q; want %q, %q",
					setting,
					reason,
					test.wantSetting,
					test.wantReason,
				)
			}
			if got != (config.HTTPServer{}) ||
				!slices.Equal(lookups, test.wantLookups) {
				t.Fatalf(
					"HTTP timeouts = %+v, lookups = %q; want zero and %q",
					got,
					lookups,
					test.wantLookups,
				)
			}
		})
	}
}
