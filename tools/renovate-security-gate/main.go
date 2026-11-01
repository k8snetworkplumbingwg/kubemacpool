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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"time"
)

var cvePattern = regexp.MustCompile(`^CVE-[0-9]{4}-[0-9]+$`)

type OSVEntry struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
}

const (
	CVSSMeetsThreshold = "MEETS_THRESHOLD"
	CVSSBelowThreshold = "BELOW_THRESHOLD"
	CVSSUnclear        = "UNCLEAR"
)

type CVSSAssessment struct {
	OSV    string
	CVE    string
	Score  *float64
	Status string
	Reason string
}

type SecurityGateResult struct {
	Fixed           []OSVEntry
	Introduced      []OSVEntry
	CVSSAssessments []CVSSAssessment
	SanityPassed    bool
	ThresholdPassed bool
}

type cvssResolver interface {
	CVSSScore(ctx context.Context, cve string) (float64, error)
}

type githubAdvisory struct {
	CVSS struct {
		Score *float64 `json:"score"`
	} `json:"cvss"`
	WithdrawnAt *string `json:"withdrawn_at"`
}

type githubAdvisoryResolver struct {
	client   *http.Client
	endpoint string
}

type findingEntry struct {
	OSV   string       `json:"osv"`
	Trace []traceFrame `json:"trace,omitempty"`
}

type traceFrame struct {
	Function string `json:"function,omitempty"`
}

type govulncheckRecord struct {
	OSV     *OSVEntry     `json:"osv,omitempty"`
	Finding *findingEntry `json:"finding,omitempty"`
}

func newGitHubAdvisoryResolver(client *http.Client) cvssResolver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &githubAdvisoryResolver{
		client:   client,
		endpoint: "https://api.github.com/advisories",
	}
}

func (resolver *githubAdvisoryResolver) CVSSScore(ctx context.Context, cve string) (float64, error) {
	client := resolver.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s?cve_id=%s", resolver.endpoint, url.QueryEscape(cve)),
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("creating GitHub Advisory request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("querying GitHub Advisory API: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return 0, fmt.Errorf("GitHub Advisory API returned %s: %s", response.Status, string(body))
	}

	var advisories []githubAdvisory
	if err := json.NewDecoder(response.Body).Decode(&advisories); err != nil {
		return 0, fmt.Errorf("decoding GitHub Advisory response: %w", err)
	}

	active := make([]githubAdvisory, 0, len(advisories))
	for _, advisory := range advisories {
		if advisory.WithdrawnAt == nil {
			active = append(active, advisory)
		}
	}
	if len(active) != 1 {
		return 0, fmt.Errorf("expected one active GitHub Advisory for %s, got %d", cve, len(active))
	}
	if active[0].CVSS.Score == nil {
		return 0, fmt.Errorf("no CVSS score for %s", cve)
	}
	return *active[0].CVSS.Score, nil
}

func diffReachable(base, candidate map[string]OSVEntry) (fixed, introduced []OSVEntry) {
	for id, entry := range base {
		if _, ok := candidate[id]; !ok {
			fixed = append(fixed, entry)
		}
	}
	for id, entry := range candidate {
		if _, ok := base[id]; !ok {
			introduced = append(introduced, entry)
		}
	}

	sort.Slice(fixed, func(i, j int) bool { return fixed[i].ID < fixed[j].ID })
	sort.Slice(introduced, func(i, j int) bool { return introduced[i].ID < introduced[j].ID })
	return fixed, introduced
}

func CVEFromOSV(entry OSVEntry) string {
	for _, alias := range entry.Aliases {
		if cvePattern.MatchString(alias) {
			return alias
		}
	}
	return ""
}

func osvIDs(entries []OSVEntry) string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return fmt.Sprintf("%v", ids)
}

func decodeReachableOSVs(input io.Reader) (map[string]OSVEntry, error) {
	decoder := json.NewDecoder(input)
	osvs := make(map[string]OSVEntry)
	findings := make(map[string]bool)
	for {
		var current govulncheckRecord
		if err := decoder.Decode(&current); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decoding govulncheck output: %w", err)
		}
		if current.OSV != nil && current.OSV.ID != "" {
			osvs[current.OSV.ID] = *current.OSV
		}
		if current.Finding != nil && current.Finding.OSV != "" &&
			len(current.Finding.Trace) > 0 && current.Finding.Trace[0].Function != "" {
			findings[current.Finding.OSV] = true
		}
	}

	reachable := make(map[string]OSVEntry, len(findings))
	for id, entry := range osvs {
		if findings[id] {
			reachable[id] = entry
		}
	}
	return reachable, nil
}

func CheckSecurityGate(ctx context.Context, base, candidate io.Reader, threshold float64, resolver cvssResolver) (*SecurityGateResult, error) {
	if resolver == nil {
		resolver = newGitHubAdvisoryResolver(nil)
	}

	baseReachable, err := decodeReachableOSVs(base)
	if err != nil {
		return nil, fmt.Errorf("decoding baseline govulncheck output: %w", err)
	}
	candidateReachable, err := decodeReachableOSVs(candidate)
	if err != nil {
		return nil, fmt.Errorf("decoding candidate govulncheck output: %w", err)
	}

	fixed, introduced := diffReachable(baseReachable, candidateReachable)
	assessments, thresholdPassed := assessFixedVulnerabilities(ctx, fixed, threshold, resolver)

	return &SecurityGateResult{
		Fixed:           fixed,
		Introduced:      introduced,
		CVSSAssessments: assessments,
		SanityPassed:    len(fixed) > 0 && len(introduced) == 0,
		ThresholdPassed: thresholdPassed,
	}, nil
}

func assessFixedVulnerabilities(ctx context.Context, fixed []OSVEntry, threshold float64, resolver cvssResolver) ([]CVSSAssessment, bool) {
	assessments := make([]CVSSAssessment, 0, len(fixed))
	thresholdPassed := false
	for _, entry := range fixed {
		assessment := CVSSAssessment{OSV: entry.ID}
		cve := CVEFromOSV(entry)
		assessment.CVE = cve
		if cve == "" {
			assessment.Status = CVSSUnclear
			assessment.Reason = "OSV has no CVE alias"
			assessments = append(assessments, assessment)
			thresholdPassed = true
			continue
		}

		score, err := resolver.CVSSScore(ctx, cve)
		if err != nil {
			assessment.Status = CVSSUnclear
			assessment.Reason = fmt.Sprintf("CVSS lookup failed: %v", err)
			assessments = append(assessments, assessment)
			thresholdPassed = true
			continue
		}

		assessment.Score = &score
		if score >= threshold {
			assessment.Status = CVSSMeetsThreshold
			thresholdPassed = true
		} else {
			assessment.Status = CVSSBelowThreshold
		}
		assessments = append(assessments, assessment)
	}

	sort.Slice(assessments, func(i, j int) bool {
		if assessments[i].CVE == assessments[j].CVE {
			return assessments[i].OSV < assessments[j].OSV
		}
		return assessments[i].CVE < assessments[j].CVE
	})
	return assessments, thresholdPassed
}

func statusLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func cvssLabel(assessment CVSSAssessment) string {
	if assessment.Score == nil {
		return "UNCLEAR"
	}
	return fmt.Sprintf("%.1f", *assessment.Score)
}

func printSecurityReport(result *SecurityGateResult, threshold float64) {
	fmt.Printf("Govulncheck sanity check: %s\n", statusLabel(result.SanityPassed))
	if len(result.Fixed) == 0 {
		fmt.Println("FAIL: Renovate CVE finding appears to be a false alarm; no baseline reachable vulnerability was fixed.")
	}
	if len(result.Introduced) > 0 {
		fmt.Printf("FAIL: Candidate introduces reachable vulnerabilities: %s\n", osvIDs(result.Introduced))
	}

	fmt.Printf("CVSS threshold check (%.1f): %s\n", threshold, statusLabel(result.ThresholdPassed))
	if !result.ThresholdPassed && len(result.CVSSAssessments) > 0 {
		fmt.Printf("FAIL: Every fixed CVE with a known CVSS score is below the %.1f threshold.\n", threshold)
	}
	for _, assessment := range result.CVSSAssessments {
		fmt.Printf(
			"%s: %s (%s) CVSS %s",
			assessment.Status,
			assessment.CVE,
			assessment.OSV,
			cvssLabel(assessment),
		)
		if assessment.Reason != "" {
			fmt.Printf(" — %s", assessment.Reason)
		}
		fmt.Println()
	}
}

func writeSecuritySummary(result *SecurityGateResult, threshold float64, output io.Writer) error {
	if _, err := fmt.Fprintf(output, "## Renovate security validation\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "### Govulncheck sanity check: %s\n\n", statusLabel(result.SanityPassed)); err != nil {
		return err
	}
	if len(result.Fixed) == 0 {
		if _, err := fmt.Fprintf(output, "- Renovate CVE finding appears to be a false alarm; no baseline reachable vulnerability was fixed.\n"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(output, "- Fixed %d reachable OSV(s): %s\n", len(result.Fixed), osvIDs(result.Fixed)); err != nil {
			return err
		}
	}
	if len(result.Introduced) > 0 {
		if _, err := fmt.Fprintf(output, "- Introduced reachable OSV(s): %s\n", osvIDs(result.Introduced)); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(output, "- Introduced no reachable OSVs\n"); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(output, "\n### CVSS threshold check (%.1f): %s\n\n", threshold, statusLabel(result.ThresholdPassed)); err != nil {
		return err
	}
	if !result.ThresholdPassed && len(result.CVSSAssessments) > 0 {
		if _, err := fmt.Fprintf(output, "- Every fixed CVE with a known CVSS score is below the %.1f threshold\n", threshold); err != nil {
			return err
		}
	}
	if len(result.CVSSAssessments) == 0 {
		if _, err := fmt.Fprintf(output, "- No fixed CVE was available to score\n"); err != nil {
			return err
		}
	}
	for _, assessment := range result.CVSSAssessments {
		cve := assessment.CVE
		if cve == "" {
			cve = assessment.OSV
		}
		if _, err := fmt.Fprintf(output, "- %s (%s): CVSS %s — %s", cve, assessment.OSV, cvssLabel(assessment), assessment.Status); err != nil {
			return err
		}
		if assessment.Reason != "" {
			if _, err := fmt.Fprintf(output, " — %s", assessment.Reason); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(output, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("renovate-security-gate", flag.ContinueOnError)
	basePath := flags.String("base", "", "path to baseline govulncheck JSON")
	candidatePath := flags.String("candidate", "", "path to candidate govulncheck JSON")
	threshold := flags.Float64("threshold", 7.0, "minimum CVSS score")
	summaryPath := flags.String("summary", "", "path to write a Markdown PR summary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *basePath == "" || *candidatePath == "" {
		return fmt.Errorf("both --base and --candidate are required")
	}
	if !(*threshold > 0 && *threshold <= 10) {
		return fmt.Errorf("--threshold must be greater than zero and at most 10")
	}

	baseFile, err := os.Open(*basePath)
	if err != nil {
		return fmt.Errorf("opening baseline output: %w", err)
	}
	defer baseFile.Close()

	candidateFile, err := os.Open(*candidatePath)
	if err != nil {
		return fmt.Errorf("opening candidate output: %w", err)
	}
	defer candidateFile.Close()

	result, err := CheckSecurityGate(context.Background(), baseFile, candidateFile, *threshold, nil)
	if err != nil {
		return err
	}
	printSecurityReport(result, *threshold)

	if *summaryPath != "" {
		summaryFile, err := os.Create(*summaryPath)
		if err != nil {
			return fmt.Errorf("creating security summary: %w", err)
		}
		if err := writeSecuritySummary(result, *threshold, summaryFile); err != nil {
			summaryFile.Close()
			return fmt.Errorf("writing security summary: %w", err)
		}
		if err := summaryFile.Close(); err != nil {
			return fmt.Errorf("closing security summary: %w", err)
		}
	}

	if !result.SanityPassed || !result.ThresholdPassed {
		return errors.New("security gate failed")
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
