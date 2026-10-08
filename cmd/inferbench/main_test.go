package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rathodpratham15/infermesh/internal/bench"
)

func fakeGateway(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("GET /v1/backends", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"queue_depth":0,"backends":[{"name":"b","healthy":true}]}`)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"a"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestRunWritesReportWithContext(t *testing.T) {
	ts := fakeGateway(t)
	out := filepath.Join(t.TempDir(), "r.json")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--target", ts.URL, "--concurrency", "1,2", "--duration", "150ms", "--stream", "--output", out,
		"--hardware", "Test box", "--engine", "fake", "--model", "tiny", "--notes", "unit test"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	rep, err := bench.ReadReport(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Levels) != 2 || rep.Levels[0].Concurrency != 1 || rep.Levels[1].Concurrency != 2 {
		t.Fatalf("levels: %+v", rep.Levels)
	}
	m := rep.Meta
	if m.Hardware != "Test box" || m.Engine != "fake" || m.Model != "tiny" || m.Notes != "unit test" || m.Target != ts.URL {
		t.Fatalf("meta not recorded: %+v", m)
	}
	if m.GatewayBefore == nil || m.GatewayAfter == nil || m.Host.NumCPU == 0 || m.Host.GoVersion == "" {
		t.Fatalf("environment snapshot missing: %+v", m)
	}
	if !rep.Config.Stream || rep.Levels[0].TTFT == nil || rep.Levels[0].OutputTokens == 0 {
		t.Fatalf("streaming results missing: %+v", rep.Levels[0])
	}
	for _, want := range []string{"hardware  Test box", "engine    fake", "ttft p50", "results written to"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("terminal output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestScenarioAndFlagPrecedence(t *testing.T) {
	ts := fakeGateway(t)
	dir := t.TempDir()
	scen := filepath.Join(dir, "s.json")
	_ = os.WriteFile(scen, []byte(`{"name":"demo","concurrency":[1,2,3],"duration":"100ms","stream":true,"max_tokens":9}`), 0o644)
	out := filepath.Join(dir, "r.json")
	var so, se bytes.Buffer
	// --concurrency given explicitly must win over the scenario's list.
	if err := run([]string{"--target", ts.URL, "--scenario", scen, "--concurrency", "2", "--output", out}, &so, &se); err != nil {
		t.Fatal(err, se.String())
	}
	rep, _ := bench.ReadReport(out)
	if len(rep.Levels) != 1 || rep.Levels[0].Concurrency != 2 {
		t.Fatalf("explicit flag should override the scenario: %+v", rep.Config.Concurrency)
	}
	if !rep.Config.Stream || rep.Config.MaxTokens != 9 || rep.Meta.Scenario != "demo" || rep.Config.Duration != "100ms" {
		t.Fatalf("scenario values not applied: %+v %+v", rep.Config, rep.Meta)
	}
}

func TestRunValidation(t *testing.T) {
	var so, se bytes.Buffer
	for name, args := range map[string][]string{
		"bad level":      {"--concurrency", "0"},
		"bad level text": {"--concurrency", "a,b"},
		"bad duration":   {"--duration", "0s"},
		"unreachable":    {"--target", "http://127.0.0.1:1", "--duration", "10ms"},
		"missing data":   {"--dataset", "/nonexistent.jsonl"},
	} {
		if err := run(args, &so, &se); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
