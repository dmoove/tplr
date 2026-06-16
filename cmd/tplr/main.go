package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/dmoove/tplr/internal/template"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		source        string
		dest          string
		outDir        string
		inPlace       bool
		env           string
		region        string
		left          string
		right         string
		ignoreMissing bool
		dryRun        bool
		showVersion   bool
	)
	flag.StringVar(&source, "source", "", "template file or glob pattern")
	flag.StringVar(&source, "file", "", "alias for -source")
	flag.StringVar(&dest, "dest", "", "output file (default stdout)")
	flag.StringVar(&dest, "out", "", "alias for -dest")
	flag.StringVar(&outDir, "out-dir", "", "output directory when rendering multiple files (template extension is stripped)")
	flag.BoolVar(&inPlace, "in-place", false, "overwrite each source file with its rendered output")
	flag.StringVar(&env, "env", os.Getenv("ENV"), "environment name")
	flag.StringVar(&region, "region", os.Getenv("AWS_REGION"), "AWS region for SSM/Secrets Manager (e.g. eusc-de-east-1 for the European Sovereign Cloud); defaults to $AWS_REGION")
	flag.StringVar(&left, "left", "{{", "left placeholder delimiter")
	flag.StringVar(&right, "right", "}}", "right placeholder delimiter")
	flag.BoolVar(&ignoreMissing, "ignore-missing", false, "leave placeholders untouched instead of failing when they cannot be resolved")
	flag.BoolVar(&dryRun, "dry-run", false, "resolve placeholders but mask secret values and write nothing")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return
	}

	if source == "" {
		log.Fatal("-source is required")
	}

	matches, err := filepath.Glob(source)
	if err != nil {
		log.Fatalf("invalid source pattern: %v", err)
	}
	if len(matches) == 0 {
		log.Fatalf("no files match %q", source)
	}
	multiple := len(matches) > 1
	if multiple && !inPlace && !dryRun && outDir == "" {
		log.Fatal("-out-dir or -in-place is required when -source matches multiple files")
	}

	opts := template.Options{
		Env:           env,
		Region:        region,
		Left:          left,
		Right:         right,
		IgnoreMissing: ignoreMissing,
		DryRun:        dryRun,
	}

	ctx := context.Background()
	for _, src := range matches {
		data, err := os.ReadFile(src)
		if err != nil {
			log.Fatalf("read %s: %v", src, err)
		}
		result, err := template.Process(ctx, string(data), opts)
		if err != nil {
			log.Fatalf("process %s: %v", src, err)
		}
		if err := writeResult(src, result, dest, outDir, inPlace, dryRun, multiple); err != nil {
			log.Fatalf("write output for %s: %v", src, err)
		}
	}
}

func writeResult(src, result, dest, outDir string, inPlace, dryRun, multiple bool) error {
	if dryRun {
		if multiple {
			fmt.Printf("# %s\n", src)
		}
		fmt.Print(result)
		if multiple && !strings.HasSuffix(result, "\n") {
			fmt.Println()
		}
		return nil
	}

	var out string
	switch {
	case inPlace:
		out = src
	case outDir != "":
		out = filepath.Join(outDir, stripTemplateExt(filepath.Base(src)))
	case dest != "":
		out = dest
	default:
		fmt.Print(result)
		return nil
	}

	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(out, []byte(result), 0o644)
}

func stripTemplateExt(name string) string {
	for _, ext := range []string{".tmpl", ".tpl", ".template"} {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext)
		}
	}
	return name
}
