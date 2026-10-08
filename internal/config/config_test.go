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

func TestRoutingSettings(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{
		"ROUTING_POLICY": "least_outstanding", "MAX_RETRIES": "3", "MAX_QUEUE": "10",
		"REQUEST_TIMEOUT": "30s", "HEALTH_INTERVAL": "1s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.RoutingPolicy != "least_outstanding" || c.MaxRetries != 3 || c.MaxQueue != 10 ||
		c.RequestTimeout != 30*time.Second || c.HealthInterval != time.Second {
		t.Fatalf("unexpected: %+v", c)
	}
	d, _ := LoadFrom(env(nil))
	if d.RoutingPolicy != "round_robin" || d.MaxRetries != 1 || d.MaxQueue != 128 {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestKafkaSettings(t *testing.T) {
	d, _ := LoadFrom(env(nil))
	if len(d.KafkaBrokers) != 0 || d.KafkaTopic != DefaultKafkaTopic {
		t.Fatalf("kafka should be disabled by default: %+v", d)
	}
	c, err := LoadFrom(env(map[string]string{"KAFKA_BROKERS": "k1:9092, k2:9092", "KAFKA_TOPIC": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.KafkaBrokers) != 2 || c.KafkaBrokers[1] != "k2:9092" || c.KafkaTopic != "t" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestAllowedOrigins(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"WS_ALLOWED_ORIGINS": "localhost:5173, example.com ,"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedOrigins) != 2 || c.AllowedOrigins[0] != "localhost:5173" || c.AllowedOrigins[1] != "example.com" {
		t.Fatalf("origins = %v", c.AllowedOrigins)
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
		"missing url":     {"BACKENDS": "vllm"},
		"unknown kind":    {"BACKENDS": "foo"},
		"duplicate":       {"BACKENDS": "mock,mock"},
		"bad weight":      {"BACKENDS": "mock", "BACKEND_MOCK_WEIGHT": "0"},
		"bad timeout":     {"BACKENDS": "mock", "BACKEND_MOCK_TIMEOUT": "soon"},
		"bad policy":      {"ROUTING_POLICY": "random"},
		"bad retries":     {"MAX_RETRIES": "-1"},
		"bad queue":       {"MAX_QUEUE": "x"},
		"bad req timeout": {"REQUEST_TIMEOUT": "0s"},
		"bad shutdown":    {"SHUTDOWN_TIMEOUT": "x"},
	}
	for name, m := range cases {
		if _, err := LoadFrom(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
