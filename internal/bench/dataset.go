package bench

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// Dataset is an ordered list of prompts. Prompts are handed out round-robin in
// a fixed order, so two runs over the same dataset send the same sequence (up to
// scheduling): benchmarks are reproducible.
type Dataset struct {
	Name    string
	Prompts []string
	next    atomic.Uint64
}

// Next returns the next prompt, safe for concurrent use.
func (d *Dataset) Next() string {
	return d.Prompts[(d.next.Add(1)-1)%uint64(len(d.Prompts))]
}

// LoadDataset reads a JSONL file ({"prompt": "..."} per line) or, for any other
// content, a plain text file with one prompt per line. Blank lines are skipped.
func LoadDataset(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	ds := &Dataset{Name: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var rec struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, n, err)
			}
			if rec.Prompt == "" {
				return nil, fmt.Errorf("%s:%d: record has no \"prompt\"", path, n)
			}
			line = rec.Prompt
		}
		ds.Prompts = append(ds.Prompts, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(ds.Prompts) == 0 {
		return nil, errors.New(path + ": dataset is empty")
	}
	return ds, nil
}

// Single returns a one-prompt dataset.
func Single(prompt string) *Dataset { return &Dataset{Name: "inline", Prompts: []string{prompt}} }
