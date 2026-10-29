package config_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func lookup(values map[string]string) config.Lookup {
	return func(name string) (string, bool) { value, exists := values[name]; return value, exists }
}

func TestValueOrDefault_DistinguishesAbsentEmptyAndExplicit(t *testing.T) {
	// given
	for _, test := range []struct {
		name    string
		values  map[string]string
		want    string
		wantErr bool
	}{
		{name: "absent", want: "fallback"},
		{name: "empty", values: map[string]string{"SETTING": ""}, wantErr: true},
		{name: "explicit", values: map[string]string{"SETTING": "override"}, want: "override"},
		{name: "whitespace retained for caller validation", values: map[string]string{"SETTING": " "}, want: " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got, err := config.ValueOrDefault(
				lookup(test.values),
				"SETTING",
				"fallback",
			)
			// then
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("value = %q, error = %v", got, err)
			}
		})
	}
}

func TestLoadAddress_DefaultPortsAndOverrides(t *testing.T) {
	// given: expected ports are specified in E03, independently of library constants.
	for _, test := range []struct{ name, fallback, want string }{
		{"HTTP_ADDR", config.DefaultHTTPAddress, "127.0.0.1:8080"},
		{"HEALTH_ADDR", config.DefaultHealthAddress, "127.0.0.1:8081"},
		{"GRPC_ADDR", config.DefaultGRPCAddress, "127.0.0.1:9090"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range []string{"", "[::1]:8181", "localhost:0"} {
				// given
				values := map[string]string{}
				want := test.want
				if value != "" {
					values[test.name] = value
					want = value
				}
				// when
				got, err := config.LoadAddress(
					lookup(values),
					test.name,
					test.fallback,
				)
				// then
				if err != nil || got.String() != want {
					t.Fatalf(
						"address = %s, error = %v, want %s",
						got,
						err,
						want,
					)
				}
				if value == "localhost:0" && got.Port() != 0 {
					t.Error("ephemeral port was changed")
				}
			}
		})
	}
}

func TestParseAddress_RejectsMalformedOrSecretValues(t *testing.T) {
	// given
	for _, value := range []string{"", "localhost", ":8080", "bad host:80", "-host:80", "host-:80", "host..test:80", "host:65536", "host:-1", "host:secret-marker", "user:secret-marker@host:80"} {
		t.Run(value, func(t *testing.T) {
			// when
			_, err := config.ParseAddress("HEALTH_ADDR", value)
			// then
			assertSafeError(t, err, "HEALTH_ADDR")
		})
	}
}

func TestValidHost_AcceptsIPAndDNSStyleNames(t *testing.T) {
	// given: these are literal compatibility expectations, including the DNS-style fallback.
	for _, test := range []struct {
		name string
		host string
	}{
		{name: "single label", host: "localhost"},
		{name: "qualified lowercase", host: "db.example"},
		{name: "mixed case and hyphen", host: "Db-01.Example"},
		{name: "ipv4", host: "127.0.0.1"},
		{name: "ipv6", host: "2001:db8::1"},
		{name: "ip shaped dns fallback", host: "127.0.0.999"},
		{name: "longest label", host: strings.Repeat("a", 63)},
		{name: "longest host", host: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got := config.ValidHost(test.host)
			// then
			if !got {
				t.Error("ValidHost rejected a supported host")
			}
		})
	}
}

func TestValidHost_RejectsMalformedOrCredentialBearingText(t *testing.T) {
	// given: rejected values never become error text because the predicate returns only bool.
	for _, test := range []struct {
		name string
		host string
	}{
		{name: "empty", host: ""},
		{name: "space", host: "db .example"},
		{name: "tab", host: "db\t.example"},
		{name: "carriage return", host: "db\r.example"},
		{name: "newline", host: "db\n.example"},
		{name: "empty label", host: "db..example"},
		{name: "trailing dot", host: "db.example."},
		{name: "leading hyphen", host: "-db.example"},
		{name: "trailing hyphen", host: "db-.example"},
		{name: "label too long", host: strings.Repeat("a", 64)},
		{name: "host too long", host: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)},
		{name: "underscore", host: "db_name.example"},
		{name: "unicode", host: "db.éxample"},
		{name: "bracketed ipv6", host: "[2001:db8::1]"},
		{name: "port", host: "db.example:5432"},
		{name: "url", host: "postgres://db.example"},
		{name: "userinfo", host: "user:secret-marker@db.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got := config.ValidHost(test.host)
			// then
			if got {
				t.Error("ValidHost accepted malformed or credential-bearing text")
			}
		})
	}
}

func TestParseValues_PreservesTypedOverrides(t *testing.T) {
	// given
	values := lookup(map[string]string{"TIMEOUT": "750ms", "LIMIT": "16"})
	// when
	duration, durationErr := config.LoadPositiveDuration(values, "TIMEOUT", "2s")
	limit, limitErr := config.LoadBoundedInt(values, "LIMIT", "8", 1, 32)
	level, levelErr := config.ParseLogLevel("LOG_LEVEL", "DEBUG+2")
	enabled, enabledErr := config.ParseBool("OTEL_ENABLED", "true")
	// then
	if durationErr != nil || duration != 750*time.Millisecond {
		t.Fatalf("duration = %v, %v", duration, durationErr)
	}
	if limitErr != nil || limit != 16 {
		t.Fatalf("limit = %v, %v", limit, limitErr)
	}
	if levelErr != nil || level != slog.LevelDebug+2 {
		t.Fatalf("level = %v, %v", level, levelErr)
	}
	if enabledErr != nil || !enabled {
		t.Fatalf("enabled = %v, %v", enabled, enabledErr)
	}
}

func TestParseValues_RejectsInvalidInputSafely(t *testing.T) {
	// given
	for _, test := range []struct {
		name    string
		parse   func(string) error
		invalid []string
	}{
		{"duration", func(value string) error {
			_, err := config.LoadPositiveDuration(lookup(map[string]string{"SETTING": value}), "SETTING", "2s")
			return err
		}, []string{"", "0s", "-1s", "secret-marker", "999999999999999999999h"}},
		{"integer", func(value string) error {
			_, err := config.LoadBoundedInt(lookup(map[string]string{"SETTING": value}), "SETTING", "8", 1, 32)
			return err
		}, []string{"", "0", "33", "1.5", "secret-marker", "9999999999999999999999"}},
		{"level", func(value string) error { _, err := config.ParseLogLevel("SETTING", value); return err }, []string{"", "secret-marker"}},
		{"boolean", func(value string) error { _, err := config.ParseBool("SETTING", value); return err }, []string{"", "secret-marker"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.invalid {
				// when
				err := test.parse(value)
				// then
				assertSafeError(t, err, "SETTING")
			}
		})
	}
}

func TestLoadBoundedInt_AcceptsInclusiveBoundsAndDefault(t *testing.T) {
	// given
	for _, value := range []string{"1", "32", ""} {
		values := map[string]string{}
		want := 8
		if value != "" {
			values["LIMIT"] = value
			if value == "1" {
				want = 1
			} else {
				want = 32
			}
		}
		// when
		got, err := config.LoadBoundedInt(lookup(values), "LIMIT", "8", 1, 32)
		// then
		if err != nil || got != want {
			t.Fatalf("limit = %d, %v; want %d", got, err, want)
		}
	}
}

func TestOptionalEndpoint_PreservesDisabledAndRequiredSemantics(t *testing.T) {
	// given
	for _, test := range []struct {
		name    string
		values  map[string]string
		enabled bool
		want    string
		wantErr bool
	}{
		{name: "disabled absent"},
		{name: "disabled empty", values: map[string]string{"ENDPOINT": ""}},
		{name: "enabled absent", enabled: true, wantErr: true},
		{name: "enabled empty", values: map[string]string{"ENDPOINT": ""}, enabled: true, wantErr: true},
		{name: "enabled normalized", values: map[string]string{"ENDPOINT": "https://alloy.monitoring.svc:4317/"}, enabled: true, want: "https://alloy.monitoring.svc:4317"},
		{name: "disabled valid override", values: map[string]string{"ENDPOINT": "http://[::1]:4317"}, want: "http://[::1]:4317"},
		{name: "disabled invalid still fails", values: map[string]string{"ENDPOINT": "secret-marker"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got, err := config.OptionalEndpoint(
				lookup(test.values),
				"ENDPOINT",
				test.enabled,
			)
			// then
			if test.wantErr {
				assertSafeError(t, err, "ENDPOINT")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if got != nil {
					t.Fatalf("endpoint = %v; want nil", got)
				}
				return
			}
			if got == nil || got.String() != test.want {
				t.Fatalf("endpoint = %v; want %s", got, test.want)
			}
		})
	}
}

func TestParseEndpoint_RejectsCredentialsAndInvalidTransport(t *testing.T) {
	// given
	for _, value := range []string{"http://user:secret-marker@collector:4317", "http://collector:4317?token=secret-marker", "http://collector:4317/#secret-marker", "http://collector:4317/v1/traces", "ftp://collector:4317", "http://collector", "http://collector:0", "http://collector:65536", "http:secret-marker", "http://%secret-marker:4317"} {
		t.Run(value, func(t *testing.T) {
			// when
			_, err := config.ParseEndpoint("ENDPOINT", value)
			// then
			assertSafeError(t, err, "ENDPOINT")
		})
	}
}

func TestErrorDetails_ExcludesUnknownAndWrapperText(t *testing.T) {
	// given
	for _, test := range []struct {
		name            string
		err             error
		setting, reason string
	}{
		{"known", config.Invalid("SETTING", "is required"), "SETTING", "is required"},
		{"wrapped", fmt.Errorf("secret-marker: %w", config.Invalid("SETTING", "is required")), "SETTING", "is required"},
		{"unknown", errors.New("secret-marker"), "unknown", "could not load configuration"},
		{"nil", nil, "unknown", "could not load configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			setting, reason := config.ErrorDetails(test.err)
			// then
			if setting != test.setting || reason != test.reason {
				t.Fatalf("details = %q %q", setting, reason)
			}
		})
	}
}

func assertSafeError(t *testing.T, err error, wantSetting string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected safe configuration failure")
	}
	setting, reason := config.ErrorDetails(err)
	if setting != wantSetting || reason == "" {
		t.Fatalf("details = %q %q", setting, reason)
	}
	for _, output := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), setting, reason} {
		if strings.Contains(output, "secret-marker") {
			t.Fatal("configuration error exposed the supplied secret")
		}
	}
}
