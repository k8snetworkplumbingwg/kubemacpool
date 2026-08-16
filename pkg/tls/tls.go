package tls

import (
	"crypto/tls"
	"fmt"
	"strings"
)

type Config struct {
	CipherSuites     []uint16
	MinTLSVersion    uint16
	GroupPreferences []tls.CurveID
}

// NewConfig can be used for aggregating raw Go crypto TLS parameters.
// The returned type can be used for overriding Go crypto/tls Config{} parameters.
func NewConfig(minTLSVersion, cipherSuites, groups string) (*Config, error) {
	minTLSVersionID, err := versionByName(minTLSVersion)
	if err != nil {
		return nil, err
	}

	cipherSuitesIDs := tlsCipherSuites(strings.Split(cipherSuites, ","))
	tlsGroupIDs := tlsGroups(strings.Split(groups, ","))

	return &Config{
		MinTLSVersion:    minTLSVersionID,
		CipherSuites:     cipherSuitesIDs,
		GroupPreferences: tlsGroupIDs,
	}, nil
}

// versionByName converts Go crypto/tls contant names of TLS version to standard TLS version ID
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

// tlsGroups translate comma-speared list of Go crypto/tls's TLS group constant names to
// TLS curve IDs matching tls.Config.CurvePreferences.
// Unknown groups are silently skipped, in case all groups are unknown TLS groups is
// selected by the runtime.
func tlsGroups(tlsGroupNames []string) []tls.CurveID {
	idByName := map[string]tls.CurveID{
		"CurveP256":          tls.CurveP256,
		"CurveP384":          tls.CurveP384,
		"CurveP521":          tls.CurveP521,
		"X25519":             tls.X25519,
		"X25519MLKEM768":     tls.X25519MLKEM768,
		"SecP256r1MLKEM768":  tls.SecP256r1MLKEM768,
		"SecP384r1MLKEM1024": tls.SecP384r1MLKEM1024,
	}
	var ids []tls.CurveID
	for _, name := range tlsGroupNames {
		if id, ok := idByName[name]; ok {
			ids = append(ids, id)
		}
	}

	return ids
}
