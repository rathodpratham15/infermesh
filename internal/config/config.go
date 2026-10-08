// Package config loads gateway settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultKafkaTopic is where the gateway publishes and the consumer reads.
const DefaultKafkaTopic = "infermesh.inference.events"

// SplitList splits a comma-separated list, trimming blanks.
func SplitList(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

type Backend struct {
	Name          string
	Kind          string // mock | vllm | triton
	URL           string
	Model         string
	Timeout       time.Duration
	Weight        int
	MaxConcurrent int
}

type Config struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
	LogLevel        string
	AllowedOrigins  []string // WebSocket origin patterns, e.g. localhost:5173
	RoutingPolicy   string
	MaxRetries      int           // extra attempts on another backend
	MaxQueue        int           // requests allowed to wait for capacity
	RequestTimeout  time.Duration // whole-request budget at the gateway
	HealthInterval  time.Duration
	KafkaBrokers    []string // empty disables telemetry events
	KafkaTopic      string
	Backends        []Backend
}

// Load reads configuration using os.Getenv.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom is Load with an injectable lookup, for tests.
//
//	LISTEN_ADDR           default :8080
//	SHUTDOWN_TIMEOUT      default 15s
//	LOG_LEVEL             debug|info|warn|error, default info
//	ROUTING_POLICY        round_robin|least_outstanding|weighted, default round_robin
//	MAX_RETRIES           retries on another backend, default 1
//	MAX_QUEUE             requests allowed to wait for capacity, default 128
//	REQUEST_TIMEOUT       whole-request budget, default 2m
//	HEALTH_INTERVAL       backend health probe period, default 5s
//	KAFKA_BROKERS         comma list; empty disables telemetry events
//	KAFKA_TOPIC           default infermesh.inference.events
//	WS_ALLOWED_ORIGINS    comma list of browser origins allowed on the WebSocket
//	BACKENDS              comma list of names, default "mock"
//	BACKEND_<NAME>_KIND   mock|vllm|triton (default: the name itself)
//	BACKEND_<NAME>_URL / _MODEL / _TIMEOUT / _WEIGHT / _MAX_CONCURRENT
func LoadFrom(get func(string) string) (Config, error) {
	c := Config{
		ListenAddr:      or(get("LISTEN_ADDR"), ":8080"),
		LogLevel:        or(get("LOG_LEVEL"), "info"),
		ShutdownTimeout: 15 * time.Second,
		RoutingPolicy:   or(get("ROUTING_POLICY"), "round_robin"),
		MaxRetries:      1,
		MaxQueue:        128,
		RequestTimeout:  2 * time.Minute,
		HealthInterval:  5 * time.Second,
		KafkaTopic:      or(get("KAFKA_TOPIC"), DefaultKafkaTopic),
		KafkaBrokers:    SplitList(get("KAFKA_BROKERS")),
	}
	switch c.RoutingPolicy {
	case "round_robin", "least_outstanding", "weighted":
	default:
		return c, fmt.Errorf("ROUTING_POLICY: unknown policy %q", c.RoutingPolicy)
	}
	c.AllowedOrigins = SplitList(get("WS_ALLOWED_ORIGINS"))
	var err error
	if v := get("SHUTDOWN_TIMEOUT"); v != "" {
		if c.ShutdownTimeout, err = time.ParseDuration(v); err != nil {
			return c, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
		}
	}

	for _, d := range []struct {
		key string
		dst *time.Duration
	}{{"REQUEST_TIMEOUT", &c.RequestTimeout}, {"HEALTH_INTERVAL", &c.HealthInterval}} {
		if v := get(d.key); v != "" {
			if *d.dst, err = time.ParseDuration(v); err != nil || *d.dst <= 0 {
				return c, fmt.Errorf("%s: must be a positive duration", d.key)
			}
		}
	}
	if c.MaxRetries, err = intEnv(get, "MAX_RETRIES", c.MaxRetries); err != nil || c.MaxRetries < 0 {
		return c, fmt.Errorf("MAX_RETRIES: must be a non-negative integer")
	}
	if c.MaxQueue, err = intEnv(get, "MAX_QUEUE", c.MaxQueue); err != nil || c.MaxQueue < 0 {
		return c, fmt.Errorf("MAX_QUEUE: must be a non-negative integer")
	}

	seen := map[string]bool{}
	for _, name := range strings.Split(or(get("BACKENDS"), "mock"), ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if seen[name] {
			return c, fmt.Errorf("BACKENDS: duplicate backend %q", name)
		}
		seen[name] = true

		p := "BACKEND_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_"
		b := Backend{
			Name:          name,
			Kind:          strings.ToLower(or(get(p+"KIND"), name)),
			URL:           get(p + "URL"),
			Model:         get(p + "MODEL"),
			Timeout:       60 * time.Second,
			Weight:        1,
			MaxConcurrent: 64,
		}
		if v := get(p + "TIMEOUT"); v != "" {
			if b.Timeout, err = time.ParseDuration(v); err != nil {
				return c, fmt.Errorf("%sTIMEOUT: %w", p, err)
			}
		}
		if b.Weight, err = intEnv(get, p+"WEIGHT", b.Weight); err != nil {
			return c, err
		}
		if b.MaxConcurrent, err = intEnv(get, p+"MAX_CONCURRENT", b.MaxConcurrent); err != nil {
			return c, err
		}
		if b.Weight < 1 || b.MaxConcurrent < 1 || b.Timeout <= 0 {
			return c, fmt.Errorf("backend %q: weight, max concurrent and timeout must be positive", name)
		}
		switch b.Kind {
		case "mock":
		case "vllm", "triton":
			if b.URL == "" {
				return c, fmt.Errorf("backend %q (%s): %sURL is required", name, b.Kind, p)
			}
		default:
			return c, fmt.Errorf("backend %q: unknown kind %q", name, b.Kind)
		}
		c.Backends = append(c.Backends, b)
	}
	if len(c.Backends) == 0 {
		return c, fmt.Errorf("BACKENDS: at least one backend is required")
	}
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func intEnv(get func(string) string, key string, def int) (int, error) {
	v := get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
