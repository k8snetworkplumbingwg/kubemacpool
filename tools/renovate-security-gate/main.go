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
	"strings"
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
	OSV    string   `json:"osv"`
	CVE    string   `json:"cve"`
	Score  *float64 `json:"score"`
	Status string   `json:"status"`
	Reason string   `json:"reason"`
}

type SecurityGateResult struct {
	Fixed           []OSVEntry       `json:"fixed"`
	Introduced      []OSVEntry       `json:"introduced"`
	CVSSAssessments []CVSSAssessment `json:"cvss_assessments"`
	SanityPassed    bool             `json:"sanity_passed"`
	ThresholdPassed bool             `json:"threshold_passed"`
	NeedsReview     bool             `json:"needs_review"`
}

var errNeedsReview = errors.New("security gate needs human review")

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
		// Best effort: the body only adds context to an error that is already being returned.
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
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

type nvdMetric struct {
	Type     string `json:"type"`
	CVSSData struct {
		BaseScore *float64 `json:"baseScore"`
	} `json:"cvssData"`
}

type nvdMetrics struct {
	CVSSMetricV40 []nvdMetric `json:"cvssMetricV40"`
	CVSSMetricV31 []nvdMetric `json:"cvssMetricV31"`
	CVSSMetricV30 []nvdMetric `json:"cvssMetricV30"`
	CVSSMetricV2  []nvdMetric `json:"cvssMetricV2"`
}

type nvdResponse struct {
	Vulnerabilities []struct {
		CVE struct {
			Metrics nvdMetrics `json:"metrics"`
		} `json:"cve"`
	} `json:"vulnerabilities"`
}

var (
	errNVDEntryNotFound = errors.New("no NVD entry")
	errNVDNoScore       = errors.New("no CVSS score in NVD")
)

type nvdResolver struct {
	client   *http.Client
	endpoint string
	retries  int
	backoff  time.Duration
}

func newNVDResolver(client *http.Client) *nvdResolver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &nvdResolver{
		client:   client,
		endpoint: "https://services.nvd.nist.gov/rest/json/cves/2.0",
		retries:  2,
		backoff:  2 * time.Second,
	}
}

func (resolver *nvdResolver) CVSSScore(ctx context.Context, cve string) (float64, error) {
	var lastErr error
	for attempt := 0; attempt <= resolver.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * resolver.backoff):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		score, err := resolver.query(ctx, cve)
		if err == nil {
			return score, nil
		}
		lastErr = err
		if errors.Is(err, errNVDEntryNotFound) || errors.Is(err, errNVDNoScore) {
			return 0, err
		}
	}
	return 0, fmt.Errorf("querying NVD API after %d attempts: %w", resolver.retries+1, lastErr)
}

func (resolver *nvdResolver) query(ctx context.Context, cve string) (float64, error) {
	client := resolver.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s?cveId=%s", resolver.endpoint, url.QueryEscape(cve)),
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("creating NVD request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("querying NVD API: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Best effort: the body only adds context to an error that is already being returned.
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return 0, fmt.Errorf("NVD API returned %s: %s", response.Status, string(body))
	}

	var payload nvdResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decoding NVD response: %w", err)
	}
	if len(payload.Vulnerabilities) != 1 {
		return 0, fmt.Errorf("%w for %s: got %d entries", errNVDEntryNotFound, cve, len(payload.Vulnerabilities))
	}
	return selectNVDScore(cve, payload.Vulnerabilities[0].CVE.Metrics)
}

// selectNVDScore prefers primary metrics and newer CVSS versions.
func selectNVDScore(cve string, metrics nvdMetrics) (float64, error) {
	groups := [][]nvdMetric{
		metrics.CVSSMetricV40,
		metrics.CVSSMetricV31,
		metrics.CVSSMetricV30,
		metrics.CVSSMetricV2,
	}
	for _, requirePrimary := range []bool{true, false} {
		for _, group := range groups {
			for _, metric := range group {
				if requirePrimary && metric.Type != "Primary" {
					continue
				}
				if metric.CVSSData.BaseScore != nil {
					return *metric.CVSSData.BaseScore, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("%w for %s", errNVDNoScore, cve)
}

type chainedResolver struct {
	resolvers []cvssResolver
}

func newChainedResolver(resolvers ...cvssResolver) cvssResolver {
	return &chainedResolver{resolvers: resolvers}
}

func (resolver *chainedResolver) CVSSScore(ctx context.Context, cve string) (float64, error) {
	messages := make([]string, 0, len(resolver.resolvers))
	for _, candidate := range resolver.resolvers {
		score, err := candidate.CVSSScore(ctx, cve)
		if err == nil {
			return score, nil
		}
		messages = append(messages, err.Error())
	}
	return 0, fmt.Errorf("CVSS lookup failed for %s: %s", cve, strings.Join(messages, "; "))
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
		resolver = newChainedResolver(newGitHubAdvisoryResolver(nil), newNVDResolver(nil))
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
	assessments, thresholdPassed, needsReview := assessFixedVulnerabilities(ctx, fixed, threshold, resolver)
	sanityPassed := len(fixed) > 0 && len(introduced) == 0

	return &SecurityGateResult{
		Fixed:           fixed,
		Introduced:      introduced,
		CVSSAssessments: assessments,
		SanityPassed:    sanityPassed,
		ThresholdPassed: thresholdPassed,
		NeedsReview:     needsReview && sanityPassed,
	}, nil
}

func assessFixedVulnerabilities(ctx context.Context, fixed []OSVEntry, threshold float64, resolver cvssResolver) ([]CVSSAssessment, bool, bool) {
	assessments := make([]CVSSAssessment, 0, len(fixed))
	thresholdPassed := false
	unclear := false
	for _, entry := range fixed {
		assessment := CVSSAssessment{OSV: entry.ID}
		cve := CVEFromOSV(entry)
		assessment.CVE = cve
		if cve == "" {
			assessment.Status = CVSSUnclear
			assessment.Reason = "OSV has no CVE alias"
			assessments = append(assessments, assessment)
			unclear = true
			continue
		}

		score, err := resolver.CVSSScore(ctx, cve)
		if err != nil {
			assessment.Status = CVSSUnclear
			assessment.Reason = fmt.Sprintf("CVSS lookup failed: %v", err)
			assessments = append(assessments, assessment)
			unclear = true
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
	return assessments, thresholdPassed, unclear && !thresholdPassed
}

func statusLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func thresholdStatusLabel(result *SecurityGateResult) string {
	switch {
	case result.ThresholdPassed:
		return "PASS"
	case result.NeedsReview:
		return "NEEDS REVIEW"
	default:
		return "FAIL"
	}
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

	fmt.Printf("CVSS threshold check (%.1f): %s\n", threshold, thresholdStatusLabel(result))
	if result.NeedsReview {
		fmt.Println("NEEDS REVIEW: CVSS score unavailable for at least one fixed CVE; human review required.")
	} else if !result.ThresholdPassed && len(result.CVSSAssessments) > 0 {
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

	if _, err := fmt.Fprintf(output, "\n### CVSS threshold check (%.1f): %s\n\n", threshold, thresholdStatusLabel(result)); err != nil {
		return err
	}
	if result.NeedsReview {
		if _, err := fmt.Fprintf(output, "- CVSS score unavailable for at least one fixed CVE; the update is held for human review\n"); err != nil {
			return err
		}
	} else if !result.ThresholdPassed && len(result.CVSSAssessments) > 0 {
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
	return runWithResolver(args, nil)
}

func runWithResolver(args []string, resolver cvssResolver) error {
	flags := flag.NewFlagSet("renovate-security-gate", flag.ContinueOnError)
	basePath := flags.String("base", "", "path to baseline govulncheck JSON")
	candidatePath := flags.String("candidate", "", "path to candidate govulncheck JSON")
	threshold := flags.Float64("threshold", 7.0, "minimum CVSS score")
	summaryPath := flags.String("summary", "", "path to write a Markdown PR summary")
	resultPath := flags.String("result", "", "path to write a machine-readable JSON result")
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

	result, err := CheckSecurityGate(context.Background(), baseFile, candidateFile, *threshold, resolver)
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

	if *resultPath != "" {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding security gate result: %w", err)
		}
		resultFile, err := os.Create(*resultPath)
		if err != nil {
			return fmt.Errorf("creating security gate result: %w", err)
		}
		if _, err := resultFile.Write(encoded); err != nil {
			resultFile.Close()
			return fmt.Errorf("writing security gate result: %w", err)
		}
		if err := resultFile.Close(); err != nil {
			return fmt.Errorf("closing security gate result: %w", err)
		}
	}

	if !result.SanityPassed {
		return errors.New("security gate failed")
	}
	if result.NeedsReview {
		return errNeedsReview
	}
	if !result.ThresholdPassed {
		return errors.New("security gate failed")
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if errors.Is(err, errNeedsReview) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}
