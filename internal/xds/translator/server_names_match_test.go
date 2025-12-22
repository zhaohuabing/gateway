// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
)

func TestAddServerNamesMatch(t *testing.T) {
	tests := []struct {
		name               string
		xdsListener        *listenerv3.Listener
		hostnames          []string
		expectMatcher      bool
		expectOnNoMatch    string
		expectMatchEntries map[string]string
		expectTLSInspector bool
	}{
		{
			name:               "nil listener",
			xdsListener:        nil,
			hostnames:          []string{"example.com"},
			expectMatcher:      false,
			expectTLSInspector: false,
		},
		{
			name: "UDP (QUIC) listener for HTTP3",
			xdsListener: &listenerv3.Listener{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Protocol: corev3.SocketAddress_UDP,
							Address:  "0.0.0.0",
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: 443,
							},
						},
					},
				},
			},
			hostnames:          []string{"example.com"},
			expectMatcher:      false,
			expectTLSInspector: false,
		},
		{
			name: "TCP listener with non-wildcard hostnames",
			xdsListener: &listenerv3.Listener{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Protocol: corev3.SocketAddress_TCP,
							Address:  "0.0.0.0",
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: 443,
							},
						},
					},
				},
			},
			hostnames:          []string{"example.com", "api.example.com"},
			expectMatcher:      true,
			expectMatchEntries: map[string]string{"example.com": "test-filter-chain", "api.example.com": "test-filter-chain"},
			expectTLSInspector: true,
		},
		{
			name: "TCP listener with wildcard hostname",
			xdsListener: &listenerv3.Listener{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Protocol: corev3.SocketAddress_TCP,
							Address:  "0.0.0.0",
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: 443,
							},
						},
					},
				},
			},
			hostnames:          []string{"*"},
			expectMatcher:      false,
			expectOnNoMatch:    "",
			expectTLSInspector: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterChain := &listenerv3.FilterChain{
				Name: "test-filter-chain",
			}

			err := addServerNamesMatch(tt.xdsListener, filterChain, tt.hostnames, true)
			require.NoError(t, err)

			require.Nil(t, filterChain.FilterChainMatch)

			// Check if TLS inspector was added
			if tt.xdsListener != nil && tt.expectTLSInspector {
				hasTLSInspector := false
				for _, filter := range tt.xdsListener.ListenerFilters {
					if filter.Name == wellknown.TlsInspector {
						hasTLSInspector = true
						break
					}
				}
				require.True(t, hasTLSInspector, "TLS inspector filter should be added")
			} else if tt.xdsListener != nil {
				// For non-nil listeners that shouldn't have TLS inspector
				hasTLSInspector := false
				for _, filter := range tt.xdsListener.ListenerFilters {
					if filter.Name == wellknown.TlsInspector {
						hasTLSInspector = true
						break
					}
				}
				require.False(t, hasTLSInspector, "TLS inspector filter should not be added")
			}

			if tt.expectMatcher {
				require.NotNil(t, tt.xdsListener.FilterChainMatcher)
				matcherTree := tt.xdsListener.FilterChainMatcher.GetMatcherTree()
				require.NotNil(t, matcherTree)
				if tt.expectOnNoMatch != "" {
					require.Equal(t, tt.expectOnNoMatch, tt.xdsListener.FilterChainMatcher.GetOnNoMatch().GetAction().GetName())
				} else {
					require.Nil(t, tt.xdsListener.FilterChainMatcher.GetOnNoMatch())
				}

				if len(tt.expectMatchEntries) > 0 {
					exactMatches := matcherTree.GetExactMatchMap()
					require.NotNil(t, exactMatches)
					for hostname, filterChainName := range tt.expectMatchEntries {
						require.Contains(t, exactMatches.GetMap(), hostname)
						require.Equal(t, filterChainName, exactMatches.GetMap()[hostname].GetAction().GetName())
					}
				}
			} else if tt.xdsListener != nil {
				require.Nil(t, tt.xdsListener.FilterChainMatcher)
			}
		})
	}
}
