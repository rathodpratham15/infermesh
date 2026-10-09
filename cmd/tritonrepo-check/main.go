// Command tritonrepo-check validates a Triton model repository for the
// TensorRT-LLM backend (structure and template mistakes; no GPU needed).
//
//	tritonrepo-check /path/to/model_repo
package main

import (
	"fmt"
	"os"

	"github.com/rathodpratham15/infermesh/internal/tritonrepo"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: tritonrepo-check <model-repository-dir>")
		os.Exit(2)
	}
	problems := tritonrepo.Check(os.Args[1])
	for _, p := range problems {
		fmt.Println(p)
	}
	if tritonrepo.HasErrors(problems) {
		fmt.Println("\nmodel repository has errors; Triton will not load it as is")
		os.Exit(1)
	}
	if len(problems) == 0 {
		fmt.Println("ok: structure looks right (engine contents and model loading are NOT checked; that needs Triton on a GPU)")
		return
	}
	fmt.Println("\nok with warnings (engine contents and model loading are NOT checked; that needs Triton on a GPU)")
}
