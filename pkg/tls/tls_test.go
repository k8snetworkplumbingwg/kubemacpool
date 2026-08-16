/*
Copyright 2026 The KubeMacPool Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tls_test

import (
	"crypto/tls"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kmptls "github.com/k8snetworkplumbingwg/kubemacpool/pkg/tls"
)

var _ = Describe("NewConfig", func() {
	It("Should fail given unknown min TLS version", func() {
		_, err := kmptls.NewConfig("VersionTLS99", "TLS_AES_128_GCM_SHA256", "X25519MLKEM768")
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("should return valid crypto package TLS parameters, given",
		func(
			minVersion, cipherSuites, groups string,
			expectedCfg *kmptls.Config,
		) {
			cfg, err := kmptls.NewConfig(minVersion, cipherSuites, groups)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg).To(Equal(expectedCfg))
		},
		Entry("TLS min version 1.0",
			"VersionTLS10", "", "",
			&kmptls.Config{MinTLSVersion: tls.VersionTLS10},
		),
		Entry("TLS min version 1.1",
			"VersionTLS11", "", "",
			&kmptls.Config{MinTLSVersion: tls.VersionTLS11},
		),
		Entry("TLS min version 1.2",
			"VersionTLS12", "", "",
			&kmptls.Config{MinTLSVersion: tls.VersionTLS12},
		),
		Entry("TLS min version 1.3",
			"VersionTLS13", "", "",
			&kmptls.Config{MinTLSVersion: tls.VersionTLS13},
		),
		Entry("unknown cipher-suite, should skip unknown cipher-suites",
			"VersionTLS13",
			"unkownCipherA,TLS_AES_128_GCM_SHA256,unkownCipherB,unkownCipherC",
			"",
			&kmptls.Config{
				MinTLSVersion: tls.VersionTLS13,
				CipherSuites:  []uint16{tls.TLS_AES_128_GCM_SHA256},
			},
		),
		Entry("unknown groups, should skip unknown groups",
			"VersionTLS13",
			"TLS_AES_128_GCM_SHA256",
			",unknownGroupA,X25519MLKEM768,unknownGroupB,unknownGroupC",
			&kmptls.Config{
				MinTLSVersion:    tls.VersionTLS13,
				CipherSuites:     []uint16{tls.TLS_AES_128_GCM_SHA256},
				GroupPreferences: []tls.CurveID{tls.X25519MLKEM768},
			},
		),
		Entry("modern TLS settings",
			"VersionTLS13",
			"TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384,TLS_CHACHA20_POLY1305_SHA256",
			"X25519MLKEM768,X25519,SecP256r1MLKEM768,SecP384r1MLKEM1024",
			&kmptls.Config{
				MinTLSVersion:    tls.VersionTLS13,
				CipherSuites:     []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256},
				GroupPreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.SecP256r1MLKEM768, tls.SecP384r1MLKEM1024},
			},
		),
	)
})
