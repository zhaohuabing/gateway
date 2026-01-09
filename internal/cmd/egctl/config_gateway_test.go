// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package egctl

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestParseGatewayResource(t *testing.T) {
	cases := []struct {
		name      string
		resource  string
		expectAll bool
		expectErr bool
	}{
		{
			name:      "default summary",
			resource:  "",
			expectAll: false,
		},
		{
			name:      "summary",
			resource:  "summary",
			expectAll: false,
		},
		{
			name:      "all",
			resource:  "all",
			expectAll: true,
		},
		{
			name:      "invalid",
			resource:  "invalid",
			expectAll: false,
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGatewayResource(tc.resource)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expectAll, got)
		})
	}
}

func TestExtractGatewayConfigDump(t *testing.T) {
	payload := []byte(`{"resources":{"foo":"bar"},"totalCount":1}`)
	fw, err := newFakePortForwarder(payload)
	require.NoError(t, err)
	require.NoError(t, fw.Start())
	defer fw.Stop()

	configDump, err := extractGatewayConfigDump(fw, true)
	require.NoError(t, err)

	resources, ok := configDump["resources"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "bar", resources["foo"])
}

func TestMarshalGatewayConfigDump(t *testing.T) {
	dump := gatewayConfigDump{
		"default": {
			"eg": map[string]interface{}{
				"resources": map[string]interface{}{
					"foo": "bar",
				},
			},
		},
	}

	jsonOut, err := marshalGatewayConfigDump(dump, "json")
	require.NoError(t, err)

	var jsonParsed gatewayConfigDump
	require.NoError(t, json.Unmarshal(jsonOut, &jsonParsed))
	require.Equal(t, dump, jsonParsed)

	yamlOut, err := marshalGatewayConfigDump(dump, "yaml")
	require.NoError(t, err)

	yamlJSON, err := yaml.YAMLToJSON(yamlOut)
	require.NoError(t, err)

	var yamlParsed gatewayConfigDump
	require.NoError(t, json.Unmarshal(yamlJSON, &yamlParsed))
	require.Equal(t, dump, yamlParsed)
}

func TestMaskSecretData(t *testing.T) {
	configDump := map[string]interface{}{
		"resources": []interface{}{
			map[string]interface{}{
				"secrets": []interface{}{
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "test",
						},
						"data": map[string]interface{}{
							"token": "value",
						},
						"stringData": map[string]interface{}{
							"password": "value",
						},
					},
				},
			},
		},
	}

	maskSecretData(configDump, nil)

	resources := configDump["resources"].([]interface{})
	resource := resources[0].(map[string]interface{})
	secrets := resource["secrets"].([]interface{})
	secret := secrets[0].(map[string]interface{})
	data := secret["data"].(map[string]interface{})
	stringData := secret["stringData"].(map[string]interface{})

	require.Equal(t, "<redacted>", data["token"])
	require.Equal(t, "<redacted>", stringData["password"])
}
