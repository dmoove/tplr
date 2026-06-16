package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dmoove/tplr/internal/template"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "completion" {
		if err := runCompletion(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}

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
		mask          bool
		validate      bool
		allowExec     bool
		timeout       time.Duration
		retries       int
		concurrency   int
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
	flag.BoolVar(&dryRun, "dry-run", false, "resolve placeholders but mask every value and write nothing")
	flag.BoolVar(&mask, "mask", false, "mask values from secret sources (SSM/Secrets Manager) in the output while writing non-secret values verbatim")
	flag.BoolVar(&validate, "validate", false, "resolve every placeholder to verify it exists, report failures and write nothing")
	flag.BoolVar(&allowExec, "allow-exec", false, "enable the cmd: source, which runs arbitrary shell commands from the template")
	flag.DurationVar(&timeout, "timeout", 0, "overall timeout for resolving placeholders (e.g. 30s); 0 means no timeout")
	flag.IntVar(&retries, "retries", 0, "maximum AWS SDK retries per request (0 uses the SDK default)")
	flag.IntVar(&concurrency, "concurrency", template.DefaultConcurrency, "maximum number of placeholder lookups resolved in parallel")
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
	if multiple && !inPlace && !dryRun && !validate && outDir == "" {
		log.Fatal("-out-dir or -in-place is required when -source matches multiple files")
	}

	opts := template.Options{
		Env:           env,
		Region:        region,
		Left:          left,
		Right:         right,
		IgnoreMissing: ignoreMissing,
		DryRun:        dryRun,
		Mask:          mask,
		AllowExec:     allowExec,
		MaxRetries:    retries,
		Concurrency:   concurrency,
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if validate {
		runValidate(ctx, matches, opts)
		return
	}

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

// runValidate resolves every placeholder in every file, reporting all failures
// without writing any output. It exits non-zero if any file fails.
func runValidate(ctx context.Context, matches []string, opts template.Options) {
	opts.IgnoreMissing = false
	failed := false
	for _, src := range matches {
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", src, err)
			failed = true
			continue
		}
		if _, err := template.Process(ctx, string(data), opts); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", src, err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s\n", src)
	}
	if failed {
		os.Exit(1)
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
