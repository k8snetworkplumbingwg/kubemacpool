package tls

import (
	"crypto/tls"
	"fmt"
	"strings"
)

type Config struct {
	CipherSuites  []uint16
	MinTLSVersion uint16
}

func NewConfig(minTLSVersion, cipherSuites string) (*Config, error) {
	minTLSVersionID, err := versionByName(minTLSVersion)
	if err != nil {
		return nil, err
	}

	cipherSuitesIDs := tlsCipherSuites(strings.Split(cipherSuites, ","))

	return &Config{
		MinTLSVersion: minTLSVersionID,
		CipherSuites:  cipherSuitesIDs,
	}, nil
}

func versionByName(name string) (uint16, error) {
	versions := map[string]uint16{
		"VersionTLS10": tls.VersionTLS10,
		"VersionTLS11": tls.VersionTLS11,
		"VersionTLS12": tls.VersionTLS12,
		"VersionTLS13": tls.VersionTLS13,
	}
	if v, ok := versions[name]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("invalid TLS version %q", name)
}

// tlsCipherSuites translate comma-speared list of OpenSSL cipher suites names and return
// the corresponding TLS cipher suites IDs matching tls.Config.CipherSuites.
func tlsCipherSuites(cipherSuitesNames []string) []uint16 {
	idByName := map[string]uint16{}
	for _, cipherSuite := range tls.CipherSuites() {
		idByName[cipherSuite.Name] = cipherSuite.ID
	}
	for _, cipherSuite := range tls.InsecureCipherSuites() {
		idByName[cipherSuite.Name] = cipherSuite.ID
	}

	var ids []uint16
	for _, name := range cipherSuitesNames {
		if id, ok := idByName[name]; ok {
			ids = append(ids, id)
		}
	}

	return ids
}
