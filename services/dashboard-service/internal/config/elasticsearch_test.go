package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_ElasticsearchDefaultsAndExplicitSettings(t *testing.T) {
	// given
	values := map[string]string{
		EnvElasticsearchURL:      "https://elasticsearch.example:9243",
		EnvElasticsearchIndex:    "policy_stats",
		EnvElasticsearchUsername: "reader",
		EnvElasticsearchPassword: "private-password",
		EnvElasticsearchCAFile:   "/var/run/elasticsearch/ca.crt",
		EnvElasticsearchTimeout:  "750ms",
	}

	// when
	defaults, defaultErr := load(mapLookup(nil))
	explicit, explicitErr := load(mapLookup(values))

	// then
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if defaults.Elasticsearch.URL != "http://127.0.0.1:9200" ||
		defaults.Elasticsearch.Index != "policy_stats" ||
		defaults.Elasticsearch.Timeout != 5*time.Second ||
		defaults.Elasticsearch.Username != "" || defaults.Elasticsearch.Password != "" ||
		defaults.Elasticsearch.CAFile != "" {
		t.Fatalf("Elasticsearch defaults = %+v", defaults.Elasticsearch)
	}
	if explicitErr != nil {
		t.Fatal(explicitErr)
	}
	if explicit.Elasticsearch.URL != values[EnvElasticsearchURL] ||
		explicit.Elasticsearch.Index != "policy_stats" ||
		explicit.Elasticsearch.Username != "reader" ||
		explicit.Elasticsearch.Password != "private-password" ||
		explicit.Elasticsearch.CAFile != "/var/run/elasticsearch/ca.crt" ||
		explicit.Elasticsearch.Timeout != 750*time.Millisecond {
		t.Fatalf("explicit Elasticsearch settings = %+v", explicit.Elasticsearch)
	}
}

func TestLoad_RejectsUnsafeElasticsearchSettingsWithoutValues(t *testing.T) {
	const privateMarker = "private-marker"
	tests := []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{
			name: "URL credentials",
			values: map[string]string{
				EnvElasticsearchURL: "https://user:" + privateMarker + "@example:9200",
			},
			setting: EnvElasticsearchURL,
		},
		{
			name: "URL path",
			values: map[string]string{
				EnvElasticsearchURL: "https://example:9200/" + privateMarker,
			},
			setting: EnvElasticsearchURL,
		},
		{
			name: "unsupported scheme",
			values: map[string]string{
				EnvElasticsearchURL: "ftp://" + privateMarker + ":9200",
			},
			setting: EnvElasticsearchURL,
		},
		{
			name: "unsafe index",
			values: map[string]string{
				EnvElasticsearchIndex: "Policy-" + privateMarker,
			},
			setting: EnvElasticsearchIndex,
		},
		{
			name:    "different index",
			values:  map[string]string{EnvElasticsearchIndex: "another-index"},
			setting: EnvElasticsearchIndex,
		},
		{
			name:    "unpaired username",
			values:  map[string]string{EnvElasticsearchUsername: privateMarker},
			setting: EnvElasticsearchUsername,
		},
		{
			name:    "unpaired password",
			values:  map[string]string{EnvElasticsearchPassword: privateMarker},
			setting: EnvElasticsearchUsername,
		},
		{
			name:    "CA over HTTP",
			values:  map[string]string{EnvElasticsearchCAFile: "/" + privateMarker},
			setting: EnvElasticsearchCAFile,
		},
		{
			name:    "oversized timeout",
			values:  map[string]string{EnvElasticsearchTimeout: "6s"},
			setting: EnvElasticsearchTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(test.values))

			// then
			if err == nil || !strings.Contains(err.Error(), test.setting) ||
				strings.Contains(err.Error(), privateMarker) {
				t.Fatalf("load error = %v", err)
			}
		})
	}
}
