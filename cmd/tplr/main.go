package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"tplr/internal/template"
)

func main() {
	var source string
	var dest string
	var env string
	flag.StringVar(&source, "source", "", "template file")
	flag.StringVar(&dest, "dest", "", "output file (default stdout)")
	flag.StringVar(&source, "file", "", "alias for -source")
	flag.StringVar(&dest, "out", "", "alias for -dest")
	flag.StringVar(&env, "env", os.Getenv("ENV"), "environment name")
	flag.Parse()

	if source == "" {
		log.Fatal("-source is required")
	}

	data, err := os.ReadFile(source)
	if err != nil {
		log.Fatalf("read file: %v", err)
	}

	ctx := context.Background()
	result, err := template.Process(ctx, string(data), env)
	if err != nil {
		log.Fatalf("process template: %v", err)
	}

	if dest == "" {
		fmt.Print(result)
	} else {
		if err := os.WriteFile(dest, []byte(result), 0o644); err != nil {
			log.Fatalf("write output: %v", err)
		}
	}
}
