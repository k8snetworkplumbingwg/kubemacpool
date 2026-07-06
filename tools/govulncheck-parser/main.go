package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	ResultAffected     = "AFFECTED"
	ResultNotReachable = "NOT_REACHABLE"
	ResultNotFound     = "NOT_FOUND"
)

type osvEntry struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
}

type findingEntry struct {
	OSV string `json:"osv"`
}

type record struct {
	OSV     *osvEntry     `json:"osv,omitempty"`
	Finding *findingEntry `json:"finding,omitempty"`
}

func stripNonJSON(data []byte) []byte {
	var buf bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '}' ||
			trimmed[0] == '"' || trimmed[0] == '[' || trimmed[0] == ']' ||
			trimmed[0] == ',' || (trimmed[0] >= '0' && trimmed[0] <= '9') ||
			bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) ||
			bytes.Equal(trimmed, []byte("null")) {
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func CheckCVE(input io.Reader, cveID string) (string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}

	data := stripNonJSON(raw)
	osvIDsForCVE := map[string]bool{}
	findingOSVIDs := map[string]bool{}

	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var rec record
		if err := decoder.Decode(&rec); err == io.EOF {
			break
		} else if err != nil {
			break
		}

		if rec.OSV != nil {
			for _, alias := range rec.OSV.Aliases {
				if alias == cveID {
					osvIDsForCVE[rec.OSV.ID] = true
				}
			}
		}

		if rec.Finding != nil && rec.Finding.OSV != "" {
			findingOSVIDs[rec.Finding.OSV] = true
		}
	}

	for id := range osvIDsForCVE {
		if findingOSVIDs[id] {
			return ResultAffected, nil
		}
	}

	if len(osvIDsForCVE) > 0 {
		return ResultNotReachable, nil
	}

	return ResultNotFound, nil
}

const (
	expectedArgs   = 2
	exitUsageError = 2
)

func main() {
	if len(os.Args) != expectedArgs {
		fmt.Fprintf(os.Stderr, "Usage: %s <CVE-ID>\n", os.Args[0])
		os.Exit(exitUsageError)
	}

	result, err := CheckCVE(os.Stdin, os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}
