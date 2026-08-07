// Command autodeploy-detect runs the production source detector over local
// archives and prints one JSON object per archive.
//
// It exists so the autodeploy benchmark (tasks/autodeploy-benchmark-50-oss.md)
// measures the detector that actually ships in the upload path, instead of a
// Python re-implementation of it that could agree with the corpus while the
// real code disagrees.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dada-tuda/console/backend/internal/sourcedetect"
)

type output struct {
	Archive   string `json:"archive"`
	Format    string `json:"format"`
	Framework string `json:"framework"`
	Port      int    `json:"port"`
	Error     string `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: autodeploy-detect <archive> [archive...]")
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	for _, path := range os.Args[1:] {
		out := output{Archive: path}
		data, err := os.ReadFile(path)
		if err != nil {
			out.Error = err.Error()
		} else if res, derr := sourcedetect.Detect(data); derr != nil {
			out.Error = derr.Error()
		} else {
			out.Format = string(res.Format)
			out.Framework = res.Framework
			out.Port = res.Port
		}
		if err := enc.Encode(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
