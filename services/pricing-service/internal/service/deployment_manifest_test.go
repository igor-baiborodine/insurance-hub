package service_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

type deploymentKustomization struct {
	Resources          []string `yaml:"resources"`
	ConfigMapGenerator []struct {
		Literals []string `yaml:"literals"`
	} `yaml:"configMapGenerator"`
}

type deploymentService struct {
	Spec struct {
		Type     string            `yaml:"type"`
		Selector map[string]string `yaml:"selector"`
		Ports    []servicePort     `yaml:"ports"`
	} `yaml:"spec"`
}

type servicePort struct {
	Name       string `yaml:"name"`
	Port       int    `yaml:"port"`
	TargetPort string `yaml:"targetPort"`
	Protocol   string `yaml:"protocol"`
}

type networkPolicyPatch struct {
	Spec struct {
		Ingress []policyIngress `yaml:"ingress"`
	} `yaml:"spec"`
}

type policyIngress struct {
	From []struct {
		PodSelector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"podSelector"`
	} `yaml:"from"`
	Ports []struct {
		Port     int    `yaml:"port"`
		Protocol string `yaml:"protocol"`
	} `yaml:"ports"`
}

func TestDeploymentManifestExposesPrivatePolicyTransports(t *testing.T) {
	// given
	root := repositoryRoot(t)
	base := filepath.Join(root, "k8s", "apps", "svc", "pricing", "base")
	var kustomization deploymentKustomization
	readYAML(t, filepath.Join(base, "kustomization.yaml"), &kustomization)
	var service deploymentService
	readYAML(t, filepath.Join(base, "service.yaml"), &service)

	// then
	if !slices.Contains(kustomization.Resources, "service.yaml") ||
		len(kustomization.ConfigMapGenerator) != 1 ||
		!slices.Contains(
			kustomization.ConfigMapGenerator[0].Literals,
			"HTTP_ADDR=0.0.0.0:8080",
		) ||
		!slices.Contains(
			kustomization.ConfigMapGenerator[0].Literals,
			"GRPC_ADDR=0.0.0.0:9090",
		) {
		t.Fatal("Pricing deployment must bind and include both private business transports")
	}
	wantSelector := map[string]string{
		"app.kubernetes.io/name":      "pricing-api",
		"app.kubernetes.io/component": "api",
	}
	wantPorts := []servicePort{
		{Name: "http", Port: 8080, TargetPort: "http", Protocol: "TCP"},
		{Name: "grpc", Port: 9090, TargetPort: "grpc", Protocol: "TCP"},
	}
	if service.Spec.Type != "ClusterIP" ||
		!reflect.DeepEqual(service.Spec.Selector, wantSelector) ||
		!reflect.DeepEqual(service.Spec.Ports, wantPorts) {
		t.Fatal("Pricing Service must expose only HTTP and gRPC to the Go API Pods")
	}

	legacyPolicy := map[string]string{
		"app.kubernetes.io/name":      "policy-api-legacy",
		"app.kubernetes.io/component": "api",
	}
	goPolicy := map[string]string{
		"app.kubernetes.io/name":      "policy-api",
		"app.kubernetes.io/component": "api",
	}
	for _, environment := range []string{"local-dev", "qa"} {
		var policy networkPolicyPatch
		readYAML(t, filepath.Join(
			root,
			"k8s",
			"overlays",
			environment,
			"svc",
			"pricing",
			"network-policy-patch.yaml",
		), &policy)
		assertPolicyIngress(t, environment, policy.Spec.Ingress, legacyPolicy, goPolicy)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readYAML(t *testing.T, path string, destination any) {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(value, destination); err != nil {
		t.Fatal(err)
	}
}

func assertPolicyIngress(
	t *testing.T,
	environment string,
	ingress []policyIngress,
	legacyPolicy map[string]string,
	goPolicy map[string]string,
) {
	t.Helper()
	if len(ingress) != 2 || len(ingress[0].From) != 1 || len(ingress[0].Ports) != 1 ||
		len(ingress[1].From) != 1 || len(ingress[1].Ports) != 1 ||
		!reflect.DeepEqual(ingress[0].From[0].PodSelector.MatchLabels, legacyPolicy) ||
		ingress[0].Ports[0].Port != 8080 || ingress[0].Ports[0].Protocol != "TCP" ||
		!reflect.DeepEqual(ingress[1].From[0].PodSelector.MatchLabels, goPolicy) ||
		ingress[1].Ports[0].Port != 9090 || ingress[1].Ports[0].Protocol != "TCP" {
		t.Fatalf("%s Pricing ingress must permit only Java HTTP and Go gRPC", environment)
	}
}
