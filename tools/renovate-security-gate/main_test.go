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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRenovateSecurityGate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Renovate Security Gate Suite")
}

type fakeCVSSResolver struct {
	scores map[string]float64
	err    error
}

func (resolver fakeCVSSResolver) CVSSScore(_ context.Context, cve string) (float64, error) {
	if resolver.err != nil {
		return 0, resolver.err
	}
	score, ok := resolver.scores[cve]
	if !ok {
		return 0, fmt.Errorf("no score configured for %s", cve)
	}
	return score, nil
}

type govulncheckInput struct {
	entry     OSVEntry
	reachable bool
}

func govulncheckJSON(inputs ...govulncheckInput) string {
	var output strings.Builder
	for _, input := range inputs {
		osv, err := json.Marshal(govulncheckRecord{OSV: &input.entry})
		Expect(err).NotTo(HaveOccurred())
		output.Write(osv)
		if input.reachable {
			finding, err := json.Marshal(govulncheckRecord{
				Finding: &findingEntry{
					OSV:   input.entry.ID,
					Trace: []traceFrame{{Function: "VulnerableFunction"}},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			output.Write(finding)
		}
	}
	return output.String()
}

func scorePtr(score float64) *float64 {
	return &score
}

var _ = Describe("CheckSecurityGate", func() {
	type testCase struct {
		base               string
		candidate          string
		threshold          float64
		resolver           fakeCVSSResolver
		expectedSanity     bool
		expectedThreshold  bool
		expectedAssessment []CVSSAssessment
		err                string
	}

	DescribeTable("should evaluate candidate branches",
		func(test testCase) {
			result, err := CheckSecurityGate(
				context.Background(),
				strings.NewReader(test.base),
				strings.NewReader(test.candidate),
				test.threshold,
				test.resolver,
			)
			if test.err != "" {
				Expect(err).To(MatchError(ContainSubstring(test.err)))
				Expect(result).To(BeNil())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SanityPassed).To(Equal(test.expectedSanity))
			Expect(result.ThresholdPassed).To(Equal(test.expectedThreshold))
			Expect(result.CVSSAssessments).To(Equal(test.expectedAssessment))
		},
		Entry("passes when a reachable high-severity CVE is fixed", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate:         govulncheckJSON(),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 7.5}},
			expectedSanity:    true,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: scorePtr(7.5), Status: CVSSMeetsThreshold},
			},
		}),
		Entry("fails the threshold when every fixed CVE is below it", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate:         govulncheckJSON(),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 6.9}},
			expectedSanity:    true,
			expectedThreshold: false,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: scorePtr(6.9), Status: CVSSBelowThreshold},
			},
		}),
		Entry("fails sanity when a candidate introduces a reachable vulnerability", testCase{
			base: govulncheckJSON(),
			candidate: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			threshold:          7.0,
			resolver:           fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 9.8}},
			expectedSanity:     false,
			expectedThreshold:  false,
			expectedAssessment: []CVSSAssessment{},
		}),
		Entry("fails sanity when a candidate fixes one CVE and introduces another", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3503", Aliases: []string{"CVE-2025-22870"}},
				reachable: true,
			}),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 7.5}},
			expectedSanity:    false,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: scorePtr(7.5), Status: CVSSMeetsThreshold},
			},
		}),
		Entry("fails sanity when the Renovate finding is a false alarm", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			threshold:          7.0,
			resolver:           fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 9.8}},
			expectedSanity:     false,
			expectedThreshold:  false,
			expectedAssessment: []CVSSAssessment{},
		}),
		Entry("passes the threshold when an OSV has no CVE alias", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"GHSA-6v2p-p543-phr9"}},
				reachable: true,
			}),
			candidate:         govulncheckJSON(),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{},
			expectedSanity:    true,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", Status: CVSSUnclear, Reason: "OSV has no CVE alias"},
			},
		}),
		Entry("passes the threshold when CVSS is unclear", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate:         govulncheckJSON(),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{err: errors.New("advisory unavailable")},
			expectedSanity:    true,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Status: CVSSUnclear, Reason: "CVSS lookup failed: advisory unavailable"},
			},
		}),
		Entry("passes the threshold when at least one fixed CVE is unclear", testCase{
			base: govulncheckJSON(
				govulncheckInput{entry: OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}}, reachable: true},
				govulncheckInput{entry: OSVEntry{ID: "GO-2025-3503", Aliases: []string{"CVE-2025-22870"}}, reachable: true},
			),
			candidate:         govulncheckJSON(),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 6.0}},
			expectedSanity:    true,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: scorePtr(6.0), Status: CVSSBelowThreshold},
				{OSV: "GO-2025-3503", CVE: "CVE-2025-22870", Status: CVSSUnclear, Reason: "CVSS lookup failed: no score configured for CVE-2025-22870"},
			},
		}),
		Entry("rejects malformed govulncheck output", testCase{
			base:      `{"osv":{"id":"BROKEN","aliases":[INVALID]}}`,
			candidate: govulncheckJSON(),
			threshold: 7.0,
			resolver:  fakeCVSSResolver{},
			err:       "decoding baseline govulncheck output",
		}),
		Entry("passes when a vulnerability becomes unreachable", testCase{
			base: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: true,
			}),
			candidate: govulncheckJSON(govulncheckInput{
				entry:     OSVEntry{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
				reachable: false,
			}),
			threshold:         7.0,
			resolver:          fakeCVSSResolver{scores: map[string]float64{"CVE-2025-22868": 7.5}},
			expectedSanity:    true,
			expectedThreshold: true,
			expectedAssessment: []CVSSAssessment{
				{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: scorePtr(7.5), Status: CVSSMeetsThreshold},
			},
		}),
	)

	Describe("decodeReachableOSVs", func() {
		It("decodes symbol-level findings", func() {
			input := `{"osv":{"id":"GO-2025-3488","aliases":["CVE-2025-22868"]}}
{"finding":{"osv":"GO-2025-3488","trace":[{"function":"VulnerableFunction"}]}}`
			entries, err := decodeReachableOSVs(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(Equal(map[string]OSVEntry{
				"GO-2025-3488": {ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}},
			}))
		})

		It("ignores findings without a matching OSV", func() {
			input := `{"osv":{"id":"GO-2025-3488","aliases":["CVE-2025-22868"]}}
{"finding":{"osv":"GO-2025-3503"}}`
			entries, err := decodeReachableOSVs(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})

		It("ignores findings without a called symbol", func() {
			input := `{"osv":{"id":"GO-2025-3488","aliases":["CVE-2025-22868"]}}
{"finding":{"osv":"GO-2025-3488","trace":[{"module":"stdlib"}]}}`
			entries, err := decodeReachableOSVs(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
	})

	Describe("githubAdvisoryResolver", func() {
		It("uses the fallback client when none is configured", func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				Expect(request.URL.Query().Get("cve_id")).To(Equal("CVE-2025-22868"))
				writer.Header().Set("Content-Type", "application/json")
				_, err := writer.Write([]byte(`[{"cvss":{"score":7.5},"withdrawn_at":null}]`))
				Expect(err).NotTo(HaveOccurred())
			}))
			defer server.Close()

			resolver := &githubAdvisoryResolver{endpoint: server.URL}
			score, err := resolver.CVSSScore(context.Background(), "CVE-2025-22868")
			Expect(err).NotTo(HaveOccurred())
			Expect(score).To(Equal(7.5))
		})

		It("treats a null advisory CVSS score as unknown", func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				Expect(request.URL.Query().Get("cve_id")).To(Equal("CVE-2025-22868"))
				writer.Header().Set("Content-Type", "application/json")
				_, err := writer.Write([]byte(`[{"cvss":{"score":null},"withdrawn_at":null}]`))
				Expect(err).NotTo(HaveOccurred())
			}))
			defer server.Close()

			resolver := &githubAdvisoryResolver{
				client:   server.Client(),
				endpoint: server.URL,
			}
			score, err := resolver.CVSSScore(context.Background(), "CVE-2025-22868")
			Expect(err).To(MatchError("no CVSS score for CVE-2025-22868"))
			Expect(score).To(Equal(0.0))
		})
	})

	Describe("run", func() {
		type testCase struct {
			threshold float64
		}

		DescribeTable("rejects invalid CVSS thresholds",
			func(test testCase) {
				err := run([]string{
					"--base", "base.json",
					"--candidate", "candidate.json",
					"--threshold", fmt.Sprintf("%v", test.threshold),
				})
				Expect(err).To(MatchError("--threshold must be greater than zero and at most 10"))
			},
			Entry("zero", testCase{threshold: 0}),
			Entry("above the CVSS maximum", testCase{threshold: 10.1}),
			Entry("NaN", testCase{threshold: math.NaN()}),
			Entry("positive infinity", testCase{threshold: math.Inf(1)}),
		)
	})

	Describe("writeSecuritySummary", func() {
		It("includes both checks and every CVSS assessment", func() {
			score := 7.5
			result := &SecurityGateResult{
				Fixed:           []OSVEntry{{ID: "GO-2025-3488", Aliases: []string{"CVE-2025-22868"}}},
				SanityPassed:    true,
				ThresholdPassed: true,
				CVSSAssessments: []CVSSAssessment{
					{OSV: "GO-2025-3488", CVE: "CVE-2025-22868", Score: &score, Status: CVSSMeetsThreshold},
				},
			}
			var summary bytes.Buffer
			Expect(writeSecuritySummary(result, 7.0, &summary)).To(Succeed())
			Expect(summary.String()).To(ContainSubstring("Govulncheck sanity check: PASS"))
			Expect(summary.String()).To(ContainSubstring("CVSS threshold check (7.0): PASS"))
			Expect(summary.String()).To(ContainSubstring("CVE-2025-22868 (GO-2025-3488): CVSS 7.5 — MEETS_THRESHOLD"))
		})
	})

	Describe("CVEFromOSV", func() {
		It("should return the first CVE alias", func() {
			Expect(CVEFromOSV(OSVEntry{
				ID:      "GO-2025-3488",
				Aliases: []string{"GHSA-6v2p-p543-phr9", "CVE-2025-22868"},
			})).To(Equal("CVE-2025-22868"))
		})

		It("should return an empty string when there is no CVE alias", func() {
			Expect(CVEFromOSV(OSVEntry{
				ID:      "GO-2025-3488",
				Aliases: []string{"GHSA-6v2p-p543-phr9"},
			})).To(BeEmpty())
		})
	})
})
