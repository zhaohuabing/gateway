//go:build boringcrypto
// +build boringcrypto

package main

import (
	"crypto/tls"
	_ "crypto/tls/fipsonly"
	"fmt"
)

func main() {
	// Create a new TLS configuration
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Now the config will only allow FIPS-compliant ciphers
	fmt.Println(config.CipherSuites)
}
