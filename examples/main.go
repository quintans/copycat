// Command-line tool example using CopyCat to process templates with a YAML model.
// This is an example of how we can create our own binary with our custom functions
// and the template embedded in the binary using Go's embed package.

package examples

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/quintans/copycat"
	"github.com/spf13/afero"
)

//go:embed template/*
var template embed.FS

func main() {
	// Command-line flags
	outputDir := flag.String("out", "", "Output directory")
	dryRun := flag.Bool("dry-run", false, "Print actions without writing files")
	flag.Parse()

	// Load model from YAML file
	model, err := copycat.LoadModel("model.yaml")
	noError(err, "failed to load model: %+v", err)

	// Ensure output directory exists (or would exist in dry-run mode)
	if *dryRun {
		fmt.Printf("DRY-RUN: would ensure output dir %s exists\n", *outputDir)
	} else {
		err = os.MkdirAll(*outputDir, 0o755)
		noError(err, "failed to create output dir: %+v", err)
	}

	cc, err := copycat.NewCopyCat(
		afero.FromIOFS{FS: template},
		afero.NewOsFs(),
		model,
		copycat.WithCustomFuncs(map[string]any{
			"slugify": func(s string) string {
				// Simple slugify implementation: lower case and replace spaces with underscores
				return strings.ReplaceAll(strings.ToLower(s), " ", "_")
			},
		}),
	)
	noError(err, "failed to create CopyCat: %+v", err)

	err = cc.Run(".", *outputDir, *dryRun)
	noError(err, "failed to process directory: %+v", err)

	if *dryRun {
		fmt.Println("Dry-run complete. No files written.")
	} else {
		fmt.Println("Template expansion complete.")
	}
}

func noError(err error, format string, a ...any) {
	if err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
