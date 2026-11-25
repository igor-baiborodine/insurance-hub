//go:build integration

// Package kafkafixture owns one disposable hostname-verified TLS Kafka broker for Policy tests.
package kafkafixture

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const defaultImage = "apache/kafka:4.1.0@sha256:bff074a5d0051dbc0bbbcd25b045bb1fe84833ec0d3c7c965d1797dd289ec88f"

// Fixture is one disposable Kafka broker and its public trust material.
type Fixture struct {
	Address string
	CACert  []byte
	broker  testcontainers.Container
}

// New starts a Kafka broker whose advertised listener is bound only to loopback.
func New(t *testing.T, ctx context.Context) *Fixture {
	t.Helper()
	port := loopbackPort(t)
	controllerPort := loopbackPort(t)
	internalPort := loopbackPort(t)
	directory := t.TempDir()
	caPath, storePath, storePasswordPath, keyPasswordPath := certificates(t, ctx, directory)
	image := os.Getenv("POLICY_TEST_KAFKA_IMAGE")
	if image == "" {
		image = defaultImage
	}
	broker, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: image,
			Env: map[string]string{
				"CLUSTER_ID":          "MkU3OEVBNTcwNTJENDM2Qk",
				"KAFKA_NODE_ID":       "1",
				"KAFKA_PROCESS_ROLES": "broker,controller",
				"KAFKA_LISTENERS": fmt.Sprintf(
					"SSL://0.0.0.0:%d,CONTROLLER://0.0.0.0:%d,INTERNAL://0.0.0.0:%d",
					port,
					controllerPort,
					internalPort,
				),
				"KAFKA_ADVERTISED_LISTENERS": fmt.Sprintf(
					"SSL://127.0.0.1:%d,INTERNAL://127.0.0.1:%d",
					port,
					internalPort,
				),
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP": "SSL:SSL,CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT",
				"KAFKA_CONTROLLER_LISTENER_NAMES":      "CONTROLLER",
				"KAFKA_CONTROLLER_QUORUM_VOTERS": fmt.Sprintf(
					"1@127.0.0.1:%d",
					controllerPort,
				),
				"KAFKA_INTER_BROKER_LISTENER_NAME":               "INTERNAL",
				"KAFKA_SSL_KEYSTORE_FILENAME":                    "server.p12",
				"KAFKA_SSL_KEYSTORE_TYPE":                        "PKCS12",
				"KAFKA_SSL_KEYSTORE_CREDENTIALS":                 "keystore-password",
				"KAFKA_SSL_KEY_CREDENTIALS":                      "key-password",
				"KAFKA_SSL_CLIENT_AUTH":                          "none",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
				"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
				"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
				"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
			},
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      storePath,
					ContainerFilePath: "/etc/kafka/secrets/server.p12",
					FileMode:          0o444,
				},
				{
					HostFilePath:      storePasswordPath,
					ContainerFilePath: "/etc/kafka/secrets/keystore-password",
					FileMode:          0o444,
				},
				{
					HostFilePath:      keyPasswordPath,
					ContainerFilePath: "/etc/kafka/secrets/key-password",
					FileMode:          0o444,
				},
			},
			NetworkMode: "host",
		},
		Started: true,
	})
	if err != nil {
		t.Fatal("start disposable Kafka:", err)
	}
	testcontainers.CleanupContainer(t, broker, testcontainers.StopTimeout(5*time.Second))
	waitForPort(t, ctx, port)
	createTopics(t, ctx, broker, internalPort)
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	return &Fixture{
		Address: fmt.Sprintf("127.0.0.1:%d", port),
		CACert:  ca,
		broker:  broker,
	}
}

func createTopics(
	t *testing.T,
	ctx context.Context,
	broker testcontainers.Container,
	internalPort int,
) {
	t.Helper()
	for _, topic := range []string{"policy-registered", "policy-terminated"} {
		deadline := time.Now().Add(2 * time.Minute)
		var lastOutput []byte
		created := false
		for time.Now().Before(deadline) {
			status, output, err := broker.Exec(ctx, []string{
				"/opt/kafka/bin/kafka-topics.sh",
				"--bootstrap-server",
				fmt.Sprintf("127.0.0.1:%d", internalPort),
				"--create",
				"--if-not-exists",
				"--partitions",
				"1",
				"--replication-factor",
				"1",
				"--topic",
				topic,
			})
			if output != nil {
				lastOutput, _ = io.ReadAll(output)
			}
			if err == nil && status == 0 {
				created = true
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if !created {
			t.Fatalf("create disposable Kafka topic %s: %s", topic, lastOutput)
		}
	}
}

func waitForPort(t *testing.T, ctx context.Context, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(
			ctx,
			"tcp4",
			fmt.Sprintf("127.0.0.1:%d", port),
		)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("disposable Kafka listener did not start")
}

// Stop simulates a broker outage without deleting its durable data.
func (fixture *Fixture) Stop(ctx context.Context) error {
	timeout := 5 * time.Second
	return fixture.broker.Stop(ctx, &timeout)
}

// Start recovers the same broker and durable data after an outage.
func (fixture *Fixture) Start(ctx context.Context) error { return fixture.broker.Start(ctx) }

func loopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func certificates(
	t *testing.T,
	ctx context.Context,
	directory string,
) (string, string, string, string) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "policy-disposable-ca"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(
		rand.Reader,
		caTemplate,
		caTemplate,
		&caKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(
		rand.Reader,
		serverTemplate,
		caTemplate,
		&serverKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(directory, "ca.crt")
	serverCertPath := filepath.Join(directory, "server.crt")
	serverKeyPath := filepath.Join(directory, "server.key")
	storePath := filepath.Join(directory, "server.p12")
	passwordSource := filepath.Join(directory, "openssl-password")
	storePassword := filepath.Join(directory, "keystore-password")
	keyPassword := filepath.Join(directory, "key-password")
	writeFile(t, caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	writeFile(
		t,
		serverCertPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
	)
	writeFile(t, serverKeyPath, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	}))
	for _, path := range []string{passwordSource, storePassword, keyPassword} {
		writeFile(t, path, []byte("changeit\n"))
	}
	command := exec.CommandContext(ctx, "openssl", "pkcs12", "-export", "-in", serverCertPath,
		"-inkey", serverKeyPath, "-certfile", caPath, "-out", storePath,
		"-passout", "file:"+passwordSource, "-name", "kafka")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create disposable Kafka keystore: %v: %s", err, output)
	}
	return caPath, storePath, storePassword, keyPassword
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
