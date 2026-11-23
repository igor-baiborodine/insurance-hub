// Package administration owns guarded Policy schema migration and read-only status.
package administration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	databaseName = "policy"
	ownerRole    = "policy"
	migratorRole = "go_policy_migrator"
	// RuntimeRole is the only database identity the serving workload may use.
	RuntimeRole = "go_policy_runtime"
	fileLimit   = 16 * 1024
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// Target describes the platform-owned enrollment for one Policy database. It holds no secret.
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
	Disposable       *bool  `json:"disposable"`
}

func readProtectedFile(path, kind string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New(kind + " must be a private regular file")
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
	if decoder.Decode(&target) != nil || decoder.More() {
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
		t.Disposable == nil || *t.Disposable != (environment == "disposable") {
		return errors.New("invalid Policy target enrollment")
	}

	var context, serviceNS, dataNS, cluster string
	switch environment {
	case "local-dev":
		context, serviceNS, dataNS = "kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"
		cluster = "local-dev-postgres-policy"
	case "qa":
		context, serviceNS, dataNS = "qa-insurance-hub", "qa-svc", "qa-data"
		cluster = "qa-postgres-policy"
	case "disposable":
		if t.DataNamespace != "" && t.ServiceNamespace != "" && t.Cluster != "" {
			return nil
		}
		return errors.New("disposable enrollment requires cluster and namespaces")
	default:
		return errors.New("unsupported Policy target environment")
	}
	if t.Context != context || t.ServiceNamespace != serviceNS || t.DataNamespace != dataNS ||
		t.Cluster != cluster || t.Host != cluster+"-rw."+dataNS+".svc.cluster.local" {
		return errors.New("enrolled Policy coordinates differ from environment policy")
	}
	return nil
}

// MigrationConnection loads the migrator credential and trust root for the enrolled host. An
// operator may reach a cluster-internal host through a loopback tunnel selected by hostaddr/port.
func (t Target) MigrationConnection(path, caPath string) (string, []byte, error) {
	content, err := readProtectedFile(path, "migrator credential")
	if err != nil {
		return "", nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(string(content)))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.User.Username() != migratorRole ||
		parsed.Hostname() != t.Host || parsed.Path != "/"+t.Database || parsed.Fragment != "" {
		return "", nil, errors.New(
			"database URL does not match enrolled Policy migrator target",
		)
	}
	if password, ok := parsed.User.Password(); !ok || password == "" {
		return "", nil, errors.New("migrator credential is missing")
	}
	if port := parsed.Port(); port != "" && port != "5432" && !*t.Disposable {
		return "", nil, errors.New("database port differs from CNPG enrollment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query["sslmode"]) != 1 || query.Get("sslmode") != "verify-full" ||
		len(
			query["sslrootcert"],
		) != 1 || caPath == "" || query.Get("sslrootcert") != caPath {
		return "", nil, errors.New(
			"database URL must use the selected CA and TLS verify-full",
		)
	}
	if len(query) != 2 {
		port, portErr := strconv.Atoi(query.Get("port"))
		if len(query) != 4 || len(query["hostaddr"]) != 1 ||
			query.Get("hostaddr") != "127.0.0.1" ||
			len(query["port"]) != 1 ||
			portErr != nil ||
			port < 1024 ||
			port > 65535 {
			return "", nil, errors.New(
				"database tunnel must use loopback and a bounded unprivileged port",
			)
		}
	}
	ca, err := readProtectedFile(caPath, "selected CA")
	if err != nil {
		return "", nil, err
	}
	return parsed.String(), ca, nil
}
