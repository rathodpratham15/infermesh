package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := LoadFrom(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || len(c.Backends) != 1 || c.Backends[0].Kind != "mock" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestMultipleBackends(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{
		"BACKENDS":                    "vllm, triton",
		"BACKEND_VLLM_URL":            "http://vllm:8000",
		"BACKEND_VLLM_MODEL":          "tiny",
		"BACKEND_VLLM_TIMEOUT":        "30s",
		"BACKEND_VLLM_WEIGHT":         "3",
		"BACKEND_VLLM_MAX_CONCURRENT": "8",
		"BACKEND_TRITON_URL":          "http://triton:8000",
	}))
	if err != nil {
		t.Fatal(err)
	}
	v := c.Backends[0]
	if v.Name != "vllm" || v.Timeout != 30*time.Second || v.Weight != 3 || v.MaxConcurrent != 8 || v.Model != "tiny" {
		t.Fatalf("unexpected vllm config: %+v", v)
	}
	if c.Backends[1].Name != "triton" || c.Backends[1].Weight != 1 {
		t.Fatalf("unexpected triton config: %+v", c.Backends[1])
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"missing url":  {"BACKENDS": "vllm"},
		"unknown kind": {"BACKENDS": "foo"},
		"duplicate":    {"BACKENDS": "mock,mock"},
		"bad weight":   {"BACKENDS": "mock", "BACKEND_MOCK_WEIGHT": "0"},
		"bad timeout":  {"BACKENDS": "mock", "BACKEND_MOCK_TIMEOUT": "soon"},
		"bad shutdown": {"SHUTDOWN_TIMEOUT": "x"},
	}
	for name, m := range cases {
		if _, err := LoadFrom(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
