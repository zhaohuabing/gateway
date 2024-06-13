// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build e2e
// +build e2e

package tests

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	"sigs.k8s.io/gateway-api/conformance/utils/http"
	"sigs.k8s.io/gateway-api/conformance/utils/kubernetes"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/envoyproxy/gateway/internal/gatewayapi"
)

func init() {
	ConformanceTests = append(ConformanceTests, OCIWasmTest)
}

// OCIWasmTest tests Wasm extension for an http route with OCI Wasm configured.
var OCIWasmTest = suite.ConformanceTest{
	ShortName:   "Wasm OCI Image Code Source",
	Description: "Test Wasm extension that adds response headers",
	Manifests:   []string{"testdata/wasm-oci.yaml", "testdata/wasm-oci-registry-test-server.yaml"},
	Test: func(t *testing.T, suite *suite.ConformanceTestSuite) {
		// Create the wasm image and envoy extension policy for the test
		setupWasmOCITest(t, suite)

		t.Run("http route with oci wasm source", func(t *testing.T) {
			ns := "gateway-conformance-infra"
			routeNN := types.NamespacedName{Name: "http-with-oci-wasm-source", Namespace: ns}
			gwNN := types.NamespacedName{Name: "same-namespace", Namespace: ns}
			gwAddr := kubernetes.GatewayAndHTTPRoutesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, suite.ControllerName, kubernetes.NewGatewayRef(gwNN), routeNN)

			ancestorRef := gwv1a2.ParentReference{
				Group:     gatewayapi.GroupPtr(gwv1.GroupName),
				Kind:      gatewayapi.KindPtr(gatewayapi.KindGateway),
				Namespace: gatewayapi.NamespacePtr(gwNN.Namespace),
				Name:      gwv1.ObjectName(gwNN.Name),
			}
			EnvoyExtensionPolicyMustBeAccepted(t, suite.Client, types.NamespacedName{Name: "oci-wasm-source-test", Namespace: ns}, suite.ControllerName, ancestorRef)

			expectedResponse := http.ExpectedResponse{
				Request: http.Request{
					Host: "www.example.com",
					Path: "/wasm-oci",
				},

				// Set the expected request properties to empty strings.
				// This is a workaround to avoid the test failure.
				// These values can't be extracted from the json format response
				// body because the test wasm code appends a "Hello, world" text
				// to the response body, invalidating the json format.
				ExpectedRequest: &http.ExpectedRequest{
					Request: http.Request{
						Host:    "",
						Method:  "",
						Path:    "",
						Headers: nil,
					},
				},
				Namespace: "",

				Response: http.Response{
					StatusCode: 200,
					Headers: map[string]string{
						"x-wasm-custom": "FOO", // response header added by wasm
					},
				},
			}

			http.MakeRequestAndExpectEventuallyConsistentResponse(t, suite.RoundTripper, suite.TimeoutConfig, gwAddr, expectedResponse)
		})

		t.Run("http route without wasm", func(t *testing.T) {
			ns := "gateway-conformance-infra"
			routeNN := types.NamespacedName{Name: "http-without-wasm", Namespace: ns}
			gwNN := types.NamespacedName{Name: "same-namespace", Namespace: ns}
			gwAddr := kubernetes.GatewayAndHTTPRoutesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, suite.ControllerName, kubernetes.NewGatewayRef(gwNN), routeNN)

			ancestorRef := gwv1a2.ParentReference{
				Group:     gatewayapi.GroupPtr(gwv1.GroupName),
				Kind:      gatewayapi.KindPtr(gatewayapi.KindGateway),
				Namespace: gatewayapi.NamespacePtr(gwNN.Namespace),
				Name:      gwv1.ObjectName(gwNN.Name),
			}
			EnvoyExtensionPolicyMustBeAccepted(t, suite.Client, types.NamespacedName{Name: "oci-wasm-source-test", Namespace: ns}, suite.ControllerName, ancestorRef)

			expectedResponse := http.ExpectedResponse{
				Request: http.Request{
					Host: "www.example.com",
					Path: "/no-wasm",
				},
				Response: http.Response{
					StatusCode:    200,
					AbsentHeaders: []string{"x-wasm-custom"},
				},
				Namespace: ns,
			}

			req := http.MakeRequest(t, &expectedResponse, gwAddr, "HTTP", "http")
			cReq, cResp, err := suite.RoundTripper.CaptureRoundTrip(req)
			if err != nil {
				t.Errorf("failed to get expected response: %v", err)
			}

			if err := http.CompareRequest(t, &req, cReq, cResp, expectedResponse); err != nil {
				t.Errorf("failed to compare request and response: %v", err)
			}
		})
	},
}

// setupWasmOCITest creates the wasm image and envoy extension policy for the test.
func setupWasmOCITest(t *testing.T, suite *suite.ConformanceTestSuite) {
	ns := "gateway-conformance-infra"
	routeNN := types.NamespacedName{Name: "oci-registry", Namespace: ns}
	gwNN := types.NamespacedName{Name: "oci-registry", Namespace: ns}
	gwAddr := kubernetes.GatewayAndHTTPRoutesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, suite.ControllerName, kubernetes.NewGatewayRef(gwNN), routeNN)

	digest, err := pushImageForWasmTest(gwAddr)
	if err != nil {
		t.Fatalf("failed to push wasm image: %v", err)
	}

	pullSecret := "registry-secret"
	if err = createPullSecretForWasmTest(suite, ns, gwAddr, pullSecret); err != nil {
		t.Fatalf("failed to create pull secret: %v", err)
	}
	if err = createEEPForWasmTest(suite, ns, gwAddr, pullSecret, digest); err != nil {
		t.Fatalf("failed to create EnvoyExtensionPoliy: %v", err)
	}
}

func createPullSecretForWasmTest(suite *suite.ConformanceTestSuite, ns, gwAddr, name string) error {
	// Define Docker credentials
	const (
		dockerUsername = "testuser"
		dockerPassword = "testpasswd"
		dockerEmail    = "your-email@example.com"
	)

	// Create Docker config JSON
	dockerConfigJSON := fmt.Sprintf(`{"auths":{"%s":{"username":"%s","password":"%s","email":"%s","auth":"%s"}}}`,
		gwAddr, dockerUsername, dockerPassword, dockerEmail,
		base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", dockerUsername, dockerPassword))))

	// Create a Secret object
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(dockerConfigJSON),
		},
	}

	// Create the secret in the specified namespace
	if err := suite.Client.Create(context.Background(), secret); err != nil {
		return err
	}
	return nil
}

func createEEPForWasmTest(suite *suite.ConformanceTestSuite, ns string, gwAddr string, pullSecret string, digest string) error {
	eep := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oci-wasm-source-test",
			Namespace: ns,
		},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			TargetRef: gwv1a2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwv1a2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  "http-with-oci-wasm-source",
				},
			},
			Wasm: []egv1a1.Wasm{
				{
					Name:   "wasm-filter",
					RootID: ptr.To("my_root_id"),
					Code: egv1a1.WasmCodeSource{
						Type: egv1a1.ImageWasmCodeSourceType,
						Image: &egv1a1.ImageWasmCodeSource{
							URL: fmt.Sprintf("%s/testwasm:v1.0.0", gwAddr),
							PullSecretRef: &gwv1b1.SecretObjectReference{
								Name: gwv1.ObjectName(pullSecret),
							},
						},
						SHA256: &digest,
					},
				},
			},
		},
	}
	if err := suite.Client.Create(context.Background(), eep); err != nil {
		return err
	}
	return nil
}

func pushImageForWasmTest(gwAddr string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	var (
		cli    *client.Client
		tar    io.Reader
		res    dockertypes.ImageBuildResponse
		digest v1.Hash
		err    error
	)

	tag := fmt.Sprintf("%s/testwasm:v1.0.0", gwAddr)

	if cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err != nil {
		return "", err
	}

	if tar, err = archive.TarWithOptions("testdata/wasm", &archive.TarOptions{}); err != nil {
		return "", err
	}

	opts := dockertypes.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
		Remove:     true,
	}
	if res, err = cli.ImageBuild(ctx, tar, opts); err != nil {
		return "", err
	}
	_ = res.Body.Close()

	ref, err := name.ParseReference(tag)
	if err != nil {
		return "", err
	}

	// Retrieve the image from the local Docker daemon
	img, err := daemon.Image(ref)
	if err != nil {
		return "", nil
	}

	const retries = 5
	for i := 0; i < retries; i++ {
		// Push the image to the remote registry
		err = crane.Push(img, tag)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", err
	}
	if img, err = remote.Image(ref); err != nil {
		return "", err
	}
	if digest, err = img.Digest(); err != nil {
		return "", err
	}
	return digest.Hex, nil
}

type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

type ErrorDetail struct {
	Message string `json:"message"`
}
