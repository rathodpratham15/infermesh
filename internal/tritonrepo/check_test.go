package tritonrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a model repository fixture. cfgs maps model -> config.pbtxt text;
// a nil entry means "directory without a config".
func repo(t *testing.T, cfgs map[string]*string) string {
	t.Helper()
	dir := t.TempDir()
	for model, cfg := range cfgs {
		must(t, os.MkdirAll(filepath.Join(dir, model, "1"), 0o755))
		if cfg != nil {
			must(t, os.WriteFile(filepath.Join(dir, model, "config.pbtxt"), []byte(*cfg), 0o644))
		}
	}
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func cfg(name, backend, enginePath string) *string {
	out := fmt.Sprintf("name: %q\n", name)
	if backend != "" {
		out += fmt.Sprintf("backend: %q\n", backend)
	}
	if enginePath != "" {
		out += fmt.Sprintf("parameters: {\n  key: \"gpt_model_path\"\n  value: {\n    string_value: %q\n  }\n}\n", enginePath)
	}
	return &out
}

func validRepo(t *testing.T) string {
	return repo(t, map[string]*string{
		"ensemble":         cfg("ensemble", "", ""),
		"preprocessing":    cfg("preprocessing", "python", ""),
		"postprocessing":   cfg("postprocessing", "python", ""),
		"tensorrt_llm":     cfg("tensorrt_llm", "tensorrtllm", "/engines/model"),
		"tensorrt_llm_bls": cfg("tensorrt_llm_bls", "python", ""),
	})
}

func messages(ps []Problem, sev Severity) string {
	var out []string
	for _, p := range ps {
		if p.Severity == sev {
			out = append(out, p.String())
		}
	}
	return strings.Join(out, "\n")
}

func TestValidRepositoryHasNoProblems(t *testing.T) {
	if ps := Check(validRepo(t)); len(ps) != 0 {
		t.Fatalf("unexpected problems: %v", ps)
	}
}

func TestMissingPieces(t *testing.T) {
	cases := map[string]struct {
		cfgs map[string]*string
		want string
	}{
		"no tensorrt_llm": {
			map[string]*string{"ensemble": cfg("ensemble", "", ""), "preprocessing": cfg("preprocessing", "python", ""), "postprocessing": cfg("postprocessing", "python", "")},
			`missing model "tensorrt_llm"`},
		"ensemble without preprocessing": {
			map[string]*string{"ensemble": cfg("ensemble", "", ""), "postprocessing": cfg("postprocessing", "python", ""), "tensorrt_llm": cfg("tensorrt_llm", "tensorrtllm", "/e")},
			`requires model "preprocessing"`},
		"missing config.pbtxt": {
			map[string]*string{"tensorrt_llm_bls": nil, "tensorrt_llm": cfg("tensorrt_llm", "tensorrtllm", "/e")},
			`config.pbtxt is missing`},
	}
	for name, c := range cases {
		ps := Check(repo(t, c.cfgs))
		if !HasErrors(ps) || !strings.Contains(messages(ps, Error), c.want) {
			t.Errorf("%s: want error containing %q, got %v", name, c.want, ps)
		}
	}
	if ps := Check(filepath.Join(t.TempDir(), "nope")); !HasErrors(ps) {
		t.Errorf("missing directory should be an error: %v", ps)
	}
}

func TestNoStandardClientFacingModelIsOnlyAWarning(t *testing.T) {
	// triton-cli style layout: client-facing model named after the model.
	dir := repo(t, map[string]*string{
		"tensorrt_llm":          cfg("tensorrt_llm", "tensorrtllm", "/tmp/engines/llama"),
		"llama-3.1-8b-instruct": cfg("llama-3.1-8b-instruct", "", ""),
	})
	ps := Check(dir)
	if HasErrors(ps) || !strings.Contains(messages(ps, Warning), "triton-cli") {
		t.Fatalf("want a warning only, got %v", ps)
	}
}

func TestUnresolvedPlaceholdersAreErrorsWithLineNumbers(t *testing.T) {
	text := "name: \"tensorrt_llm\"\nbackend: \"tensorrtllm\"\nparameters: {\n  key: \"gpt_model_path\"\n  value: {\n    string_value: \"${engine_dir}\"\n  }\n}\nmax_batch_size: ${triton_max_batch_size}\n"
	dir := repo(t, map[string]*string{"tensorrt_llm": &text, "tensorrt_llm_bls": cfg("tensorrt_llm_bls", "python", "")})
	errs := messages(Check(dir), Error)
	for _, want := range []string{"${engine_dir}", "${triton_max_batch_size}", "config.pbtxt:6", "config.pbtxt:9"} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
}

func TestWrongBackendAndNameMismatch(t *testing.T) {
	dir := repo(t, map[string]*string{
		"tensorrt_llm":     cfg("tensorrt_llm", "python", "/e"),
		"tensorrt_llm_bls": cfg("something_else", "python", ""),
	})
	errs := messages(Check(dir), Error)
	if !strings.Contains(errs, `backend must be "tensorrtllm", found python`) || !strings.Contains(errs, `does not match directory "tensorrt_llm_bls"`) {
		t.Fatalf("errors:\n%s", errs)
	}
}

func TestEnginePathWarningsAndErrors(t *testing.T) {
	base := map[string]*string{"tensorrt_llm_bls": cfg("tensorrt_llm_bls", "python", "")}
	with := func(c *string) []Problem {
		m := map[string]*string{"tensorrt_llm_bls": base["tensorrt_llm_bls"], "tensorrt_llm": c}
		return Check(repo(t, m))
	}
	// Parameter missing: version-dependent name, so only a warning.
	ps := with(cfg("tensorrt_llm", "tensorrtllm", ""))
	if HasErrors(ps) || !strings.Contains(messages(ps, Warning), "gpt_model_path") {
		t.Errorf("missing param should warn only: %v", ps)
	}
	// Empty path is always wrong.
	empty := "name: \"tensorrt_llm\"\nbackend: \"tensorrtllm\"\nparameters: {\n  key: \"gpt_model_path\"\n  value: {\n    string_value: \"\"\n  }\n}\n"
	if ps := with(&empty); !strings.Contains(messages(ps, Error), "is empty") {
		t.Errorf("empty path should be an error: %v", ps)
	}
	// Relative path: warning.
	if ps := with(cfg("tensorrt_llm", "tensorrtllm", "engines/model")); HasErrors(ps) || !strings.Contains(messages(ps, Warning), "not absolute") {
		t.Errorf("relative path should warn only: %v", ps)
	}
}

func TestVersionDirectoryRequired(t *testing.T) {
	dir := validRepo(t)
	must(t, os.RemoveAll(filepath.Join(dir, "preprocessing", "1")))
	if errs := messages(Check(dir), Error); !strings.Contains(errs, "no numeric version directory") {
		t.Fatalf("errors:\n%s", errs)
	}
}
