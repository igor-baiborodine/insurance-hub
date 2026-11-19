package administration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const (
	databaseName = "pricing"
	migratorRole = "go_pricing_migrator"
	seederRole   = "go_pricing_seeder"
	fileLimit    = 16 * 1024
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// Target describes the platform-owned enrollment for one Pricing database.
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
	SeedAllowed      *bool  `json:"seedAllowed"`
	Disposable       *bool  `json:"disposable"`
}

func readProtectedFile(path, kind string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New(kind + " must be a protected regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open " + kind)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, fileLimit+1))
	if err != nil || len(content) > fileLimit {
		return nil, errors.New("cannot read " + kind + " within size limit")
	}
	return content, nil
}

// ReadTarget loads and validates the selected deployment's protected enrollment.
func ReadTarget(path, environment, contextName string) (Target, error) {
	content, err := readProtectedFile(path, "target enrollment")
	if err != nil {
		return Target{}, err
	}
	var target Target
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if !json.Valid(content) || decoder.Decode(&target) != nil {
		return Target{}, errors.New("invalid target enrollment")
	}
	if err := target.validate(environment, contextName); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (t Target) validate(environment, contextName string) error {
	if t.Environment != environment || t.Context != contextName || t.Context == "" {
		return errors.New("requested environment or context does not match enrollment")
	}
	if t.Database != databaseName || t.Host == "" || strings.ContainsAny(t.Host, ",/") ||
		!uuidPattern.MatchString(t.InstanceID) || !uuidPattern.MatchString(t.ClusterUID) ||
		t.SeedAllowed == nil || t.Disposable == nil || *t.Disposable != (environment == "disposable") {
		return errors.New("invalid Pricing target enrollment")
	}

	var context, serviceNS, dataNS, cluster string
	switch environment {
	case "local-dev":
		context, serviceNS, dataNS = "kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"
		cluster = "local-dev-postgres-pricing"
	case "qa":
		context, serviceNS, dataNS = "qa-insurance-hub", "qa-svc", "qa-data"
		cluster = "qa-postgres-pricing"
	case "disposable":
		if t.DataNamespace != "" && t.ServiceNamespace != "" && t.Cluster != "" {
			return nil
		}
		return errors.New("disposable enrollment requires cluster and namespaces")
	default:
		return errors.New("unsupported Pricing target environment")
	}
	if t.Context != context || t.ServiceNamespace != serviceNS || t.DataNamespace != dataNS ||
		t.Cluster != cluster || t.Host != cluster+"-rw."+dataNS+".svc.cluster.local" {
		return errors.New("enrolled Pricing coordinates differ from environment policy")
	}
	return nil
}

// MigrationConnection loads the migrator credential and trust root for the enrolled host.
func (t Target) MigrationConnection(path, caPath string) (string, []byte, error) {
	return t.connection(path, caPath, migratorRole, "migrator")
}

// SeedConnection requires seed approval and the dedicated seeder credential.
func (t Target) SeedConnection(path, caPath string) (string, []byte, error) {
	if t.SeedAllowed == nil || !*t.SeedAllowed {
		return "", nil, errors.New("seeding is disabled for the Pricing enrollment")
	}
	return t.connection(path, caPath, seederRole, "seeder")
}

func (t Target) connection(path, caPath, role, kind string) (string, []byte, error) {
	content, err := readProtectedFile(path, kind+" credential")
	if err != nil {
		return "", nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(string(content)))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.User.Username() != role ||
		parsed.Hostname() != t.Host || parsed.Path != "/"+t.Database || parsed.Fragment != "" {
		return "", nil, errors.New(
			"database URL does not match enrolled Pricing " + kind + " target",
		)
	}
	if password, ok := parsed.User.Password(); !ok || password == "" {
		return "", nil, errors.New(kind + " credential is missing")
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	if !*t.Disposable && port != "5432" {
		return "", nil, errors.New("database port differs from CNPG enrollment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 2 || len(query["sslmode"]) != 1 ||
		query.Get("sslmode") != "verify-full" || len(query["sslrootcert"]) != 1 ||
		caPath == "" || query.Get("sslrootcert") != caPath {
		return "", nil, errors.New(
			"database URL must use the selected CA and TLS verify-full",
		)
	}
	parsed.Host = net.JoinHostPort(t.Host, port)
	ca, err := readProtectedFile(caPath, "selected CA")
	if err != nil {
		return "", nil, err
	}
	return parsed.String(), ca, nil
}
