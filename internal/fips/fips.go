// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fips

import (
	gotls "crypto/tls"
	"strings"

	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
)

// enable will be set to "true" by the makefile if the binary is built with the FIPS build tag.
var enable string

var fipsCiphers = []string{
	"ECDHE-ECDSA-AES128-GCM-SHA256",
	"ECDHE-RSA-AES128-GCM-SHA256",
	"ECDHE-ECDSA-AES256-GCM-SHA384",
	"ECDHE-RSA-AES256-GCM-SHA384",
}

var fipsGoCiphers = []uint16{
	gotls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	gotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	gotls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	gotls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
}

func index(ciphers []string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, cipher := range ciphers {
		out[cipher] = struct{}{}
	}
	return out
}

var fipsCipherIndex = index(fipsCiphers)

// EnforceGoFIPSCompliance limits the Golang TLS settings to the compliant values.
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/transport_sockets/tls/v3/common.proto#envoy-v3-api-field-extensions-transport-sockets-tls-v3-tlsparameters-cipher-suites
func EnforceGoFIPSCompliance(config *gotls.Config) *gotls.Config {
	if !Enable() {
		return config
	}

	config.MinVersion = gotls.VersionTLS12
	config.MaxVersion = gotls.VersionTLS12
	config.CipherSuites = fipsGoCiphers
	config.CurvePreferences = []gotls.CurveID{gotls.CurveP256}
	return config
}

// EnforceXDSFIPSCompliance limits the xDS TLS settings to the compliant values.
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/transport_sockets/tls/v3/common.proto#envoy-v3-api-field-extensions-transport-sockets-tls-v3-tlsparameters-cipher-suites
func EnforceXDSFIPSCompliance(ctx *tls.CommonTlsContext) *tls.CommonTlsContext {
	if !Enable() {
		return ctx
	}

	if ctx.TlsParams == nil {
		ctx.TlsParams = &tls.TlsParameters{}
	}
	ctx.TlsParams.TlsMinimumProtocolVersion = tls.TlsParameters_TLSv1_2
	ctx.TlsParams.TlsMaximumProtocolVersion = tls.TlsParameters_TLSv1_2
	// Default (unset) cipher suites field in the FIPS build of Envoy uses only the FIPS ciphers.
	// Therefore, we only filter this field when it is set.
	if len(ctx.TlsParams.CipherSuites) > 0 {
		ciphers := []string{}
		for _, cipher := range ctx.TlsParams.CipherSuites {
			if _, ok := fipsCipherIndex[cipher]; ok {
				ciphers = append(ciphers, cipher)
			}
		}
		ctx.TlsParams.CipherSuites = ciphers
	}
	// Default (unset) is P-256
	ctx.TlsParams.EcdhCurves = nil
	return ctx
}

func Enable() bool {
	return strings.EqualFold(enable, "true")
}
