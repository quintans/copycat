package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/quintans/copycat"
	"github.com/spf13/afero"
)

func main() {
	// Command-line flags
	modelFile := flag.String("model", "", "YAML model file")
	templateDir := flag.String("template", "", "Template directory")
	outputDir := flag.String("out", "", "Output directory")
	dryRun := flag.Bool("dry-run", false, "Print actions without writing files")
	flag.Parse()

	// Load model from YAML file
	model, err := copycat.LoadModel(*modelFile)
	noError(err, "failed to load model: %+v", err)

	info, err := os.Stat(*templateDir)
	noError(err, "template dir error: %+v", err)
	if !info.IsDir() {
		fatalf("template path must be a directory")
	}

	// Ensure output directory exists (or would exist in dry-run mode)
	if *dryRun {
		fmt.Printf("DRY-RUN: would ensure output dir %s exists\n", *outputDir)
	} else {
		err = os.MkdirAll(*outputDir, 0o755)
		noError(err, "failed to create output dir: %+v", err)
	}

	cc, err := copycat.NewCopyCat(
		afero.NewOsFs(),
		afero.NewOsFs(),
		model,
	)
	noError(err, "failed to create CopyCat: %+v", err)

	err = cc.Run(*templateDir, *outputDir, *dryRun)
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

	fatalf(format, a...)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
