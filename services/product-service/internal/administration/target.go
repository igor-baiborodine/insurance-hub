package administration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var instanceIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// Target is a platform-owned enrollment. It contains no password or connection string.
type Target struct {
	Environment      string `json:"environment"`
	Context          string `json:"context"`
	DataNamespace    string `json:"dataNamespace"`
	ServiceNamespace string `json:"serviceNamespace"`
	Cluster          string `json:"cluster"`
	ClusterUID       string `json:"clusterUID"`
	InstanceID       string `json:"instanceID"`
	Database         string `json:"database"`
	Host             string `json:"host"`
	SeedAllowed      bool   `json:"seedAllowed"`
	Disposable       bool   `json:"disposable"`
}

// ReadTarget rejects unknown enrollment fields rather than silently relaxing a target policy.
func ReadTarget(path string) (Target, error) {
	if path == "" {
		return Target{}, errors.New("target enrollment path is required")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Target{}, fmt.Errorf("open target enrollment: %w", err)
	}
	if len(content) > 16*1024 {
		return Target{}, errors.New("target enrollment is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var target Target
	if err := decoder.Decode(&target); err != nil {
		return Target{}, fmt.Errorf("decode target enrollment: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return Target{}, errors.New("target enrollment has trailing content")
	}
	for _, key := range []string{
		"environment", "context", "dataNamespace", "serviceNamespace",
		"cluster", "clusterUID", "instanceID", "database", "host", "seedAllowed", "disposable",
	} {
		if _, present := fields[key]; !present {
			return Target{}, fmt.Errorf("target enrollment missing %s", key)
		}
	}
	return target, nil
}

// ValidateMigrationConnection checks the selected target and migrator connection before a dial.
func (t Target) ValidateMigrationConnection(
	selectedEnvironment,
	selectedContext,
	databaseURL,
	caPath string,
) error {
	return t.validateCredential(
		selectedEnvironment,
		selectedContext,
		databaseURL,
		caPath,
		"go_product_migrator",
		"migrator",
	)
}

// ValidateSeedConnection checks the selected target and seeder connection before a dial.
func (t Target) ValidateSeedConnection(
	selectedEnvironment,
	selectedContext,
	databaseURL,
	caPath string,
) error {
	if t.Environment == "production" || !t.SeedAllowed {
		return errors.New("target enrollment does not allow seeding")
	}
	return t.validateCredential(
		selectedEnvironment,
		selectedContext,
		databaseURL,
		caPath,
		"go_product_seeder",
		"seed",
	)
}

func (t Target) validateCredential(
	selectedEnvironment,
	selectedContext,
	databaseURL,
	caPath,
	expectedUser,
	operation string,
) error {
	if selectedEnvironment == "" || selectedContext == "" ||
		t.Environment != selectedEnvironment || t.Context != selectedContext {
		return errors.New("target environment or context does not match enrollment")
	}
	if t.Environment == "" || t.Context == "" || t.DataNamespace == "" ||
		t.ServiceNamespace == "" || t.Cluster == "" || t.ClusterUID == "" ||
		t.InstanceID == "" || t.Host == "" || t.Database != "product" || caPath == "" {
		return errors.New("target enrollment is incomplete")
	}
	if !instanceIDPattern.MatchString(t.InstanceID) {
		return errors.New("target instance UUID is invalid")
	}
	if t.Environment == "production" && t.SeedAllowed {
		return errors.New("production enrollment cannot allow seeding")
	}
	if !t.Disposable {
		expectedHost := t.Cluster + "-rw." + t.DataNamespace + ".svc.cluster.local"
		if t.Host != expectedHost {
			return errors.New("enrolled host does not match CNPG coordinates")
		}
		switch t.Environment {
		case "local-dev":
			if t.Context != "kind-local-dev-insurance-hub" ||
				t.DataNamespace != "local-dev-all" || t.ServiceNamespace != "local-dev-all" ||
				t.Cluster != "local-dev-postgres-product" {
				return errors.New(
					"local-dev coordinates differ from enrollment policy",
				)
			}
		case "qa":
			if t.Context != "qa-insurance-hub" || t.DataNamespace != "qa-data" ||
				t.ServiceNamespace != "qa-svc" || t.Cluster != "qa-postgres-product" {
				return errors.New("QA coordinates differ from enrollment policy")
			}
		default:
			// New targets, including production, require this explicit platform enrollment.
		}
	}
	if t.Disposable != (t.Environment == "disposable") {
		return errors.New("disposable target classification is inconsistent")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil ||
		parsed.User.Username() != expectedUser || parsed.Hostname() != t.Host ||
		strings.TrimPrefix(parsed.Path, "/") != t.Database || parsed.Fragment != "" {
		return fmt.Errorf("database URL does not match enrolled %s target", operation)
	}
	if password, hasPassword := parsed.User.Password(); !hasPassword || password == "" {
		return fmt.Errorf("%s credential is missing", operation)
	}
	if port := parsed.Port(); port != "" && port != "5432" && !t.Disposable {
		return errors.New("database port differs from CNPG enrollment")
	}
	query := parsed.Query()
	if len(query) != 2 || query.Get("sslmode") != "verify-full" ||
		query.Get("sslrootcert") != caPath || len(query["sslmode"]) != 1 ||
		len(query["sslrootcert"]) != 1 {
		return errors.New("database URL must use the selected CA and TLS verify-full")
	}
	return nil
}
