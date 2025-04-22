package main

import (
	"better-docs/route"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"better-docs/indexing"
	"github.com/spf13/cobra"
)

var (
	specFile   string
	staticDir  string
	indexFile  string
	listenHost string
	listenPort int
	timeout    time.Duration
	cacheDir   string
)

func run(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		log.Fatalf("failed to create cache directory %q: %v", cacheDir, err)
	}

	cachePath := path.Join(cacheDir, "bleve")
	reg, err := indexing.LoadConfigAndIndex(ctx, specFile, cachePath)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}

	idx, err := indexing.BuildShardedIndices(cacheDir, indexing.NewIndexMapping(), reg)
	if err != nil {
		return fmt.Errorf("failed to build Bleve index: %w", err)
	}

	svc := route.NewSearchService(reg, idx)
	ac := route.NewActionService(reg, idx)

	specs, proxyMap, err := route.LoadSpecs(specFile)
	if err != nil {
		return fmt.Errorf("failed to load specs: %w", err)
	}

	httpClient := &http.Client{Timeout: timeout}
	mux := http.NewServeMux()
	route.RegisterRoutes(mux, specs, proxyMap, httpClient, staticDir, indexFile, svc, ac)

	addr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	log.Printf("Listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func main() {
	root := &cobra.Command{
		Use:   "better-docs",
		Short: "Better Centralized API Documentation",
		RunE:  run,
	}

	root.Flags().StringVar(&specFile, "specs", "specs.json", "path to specs JSON file")
	root.Flags().StringVar(&staticDir, "static-dir", "static", "directory for static files")
	root.Flags().StringVar(&indexFile, "index", "index.html", "path to SPA index file")
	root.Flags().StringVar(&listenHost, "host", "127.0.0.1", "listen address")
	root.Flags().IntVar(&listenPort, "port", 5001, "listen port")
	root.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "timeout for proxied requests")
	root.Flags().StringVar(&cacheDir, "cache", ".bleveIndexes", "path to indexing cache file")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
