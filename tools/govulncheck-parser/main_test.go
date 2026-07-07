package main_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	parser "github.com/k8snetworkplumbingwg/kubemacpool/tools/govulncheck-parser"
)

func TestGovulncheckParser(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Govulncheck Parser Suite")
}

var _ = Describe("CheckCVE", func() {
	type testCase struct {
		input    string
		cveID    string
		expected string
	}

	DescribeTable("should correctly classify CVEs",
		func(tc testCase) {
			result, err := parser.CheckCVE(strings.NewReader(tc.input), tc.cveID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(tc.expected))
		},
		Entry("affected - CVE has matching OSV and finding", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultAffected,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}
{"finding": {"osv": "GO-2025-3488"}}`,
		}),
		Entry("not reachable - CVE has OSV but no finding", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotReachable,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}`,
		}),
		Entry("not found - CVE not in any OSV entry", testCase{
			cveID:    "CVE-9999-99999",
			expected: parser.ResultNotFound,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}`,
		}),
		Entry("not found - empty output", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotFound,
			input:    `{"config": {"scanner_name": "govulncheck"}}`,
		}),
		Entry("affected - multiple OSV entries, one matches", testCase{
			cveID:    "CVE-2025-22870",
			expected: parser.ResultAffected,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}
{"osv": {"id": "GO-2025-3503", "aliases": ["CVE-2025-22870"]}}
{"finding": {"osv": "GO-2025-3503"}}`,
		}),
		Entry("not reachable - finding exists for different OSV", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotReachable,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}
{"osv": {"id": "GO-2025-3503", "aliases": ["CVE-2025-22870"]}}
{"finding": {"osv": "GO-2025-3503"}}`,
		}),
		Entry("non-JSON lines before JSON are skipped", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotReachable,
			input: `/path/to/govulncheck -format json ./...
{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}`,
		}),
		Entry("non-JSON lines between JSON records are skipped", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultAffected,
			input: `some log line
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}
another log line
{"finding": {"osv": "GO-2025-3488"}}`,
		}),
		Entry("affected - CVE is one of multiple aliases", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultAffected,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868", "GHSA-6v2p-p543-phr9"]}}
{"finding": {"osv": "GO-2025-3488"}}`,
		}),
		Entry("empty finding.osv is ignored", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotReachable,
			input: `{"config": {"scanner_name": "govulncheck"}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}
{"finding": {"osv": ""}}`,
		}),
		Entry("malformed JSON record stops parsing", testCase{
			cveID:    "CVE-2025-22868",
			expected: parser.ResultNotFound,
			input: `{"osv": {"id": "BROKEN", "aliases": [INVALID]}}
{"osv": {"id": "GO-2025-3488", "aliases": ["CVE-2025-22868"]}}`,
		}),
	)
})
