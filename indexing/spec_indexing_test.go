package indexing_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"better-docs/indexing"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestLoadConfigAndFindOperation(t *testing.T) {
	tmpDir := t.TempDir()

	specJSON := `{
	  "openapi": "3.0.0",
	  "info": { "title": "TestSpec API", "version": "1.0.0" },
	  "servers": [{ "url": "http://example.com/api" }],
	  "paths": {
	    "/items/{id}": {
	      "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
	      "get": {
	        "operationId": "getItem",
	        "responses": { "200": { "description": "OK" } }
	      }
	    }
	  }
	}`
	specPath := writeFile(t, tmpDir, "test-spec.json", specJSON)

	cfg := []indexing.SpecConfig{{
		DisplayName: "TestSpec",
		Name:        "test-spec",
		File:        specPath,
		URL:         "",
	}}
	cfgBytes, err := json.Marshal(cfg)
	require.NoError(t, err)
	cfgPath := writeFile(t, tmpDir, "specs.json", string(cfgBytes))

	cachePath := filepath.Join(tmpDir, "cache.gob")
	reg, err := indexing.LoadConfigAndIndex(context.Background(), cfgPath, cachePath)
	require.NoError(t, err)

	// Build on‐disk shards + alias
	idxDir := filepath.Join(tmpDir, "bleve_indexes")
	idx, err := indexing.BuildShardedIndices(idxDir, indexing.NewIndexMapping(), reg)
	require.NoError(t, err)

	// Valid GET
	specName, opID, _, err := indexing.FindOperation(idx, reg, "GET", "http://example.com/api/items/123?foo=bar")
	require.NoError(t, err)
	require.Equal(t, "test-spec", specName)
	require.Equal(t, "getItem", opID)

	// Unsupported method
	_, _, _, err = indexing.FindOperation(idx, reg, "POST", "http://example.com/api/items/123")
	require.Error(t, err)

	// Unknown path
	_, _, _, err = indexing.FindOperation(idx, reg, "GET", "http://example.com/api/unknown/1")
	require.Error(t, err)
}

func TestBleveSearch(t *testing.T) {
	tmpDir := t.TempDir()

	specJSON := `{
	  "openapi": "3.0.0",
	  "info": { "title": "SearchSpec API", "version": "1.0.0" },
	  "servers": [{ "url": "http://search.test/api" }],
	  "paths": {
	    "/things/{id}": {
	      "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
	      "get": {
	        "operationId": "getThing",
	        "summary": "Retrieve a thing",
	        "description": "Detailed description",
	        "tags": ["alpha","beta"],
	        "responses": { "200": { "description": "OK" } }
	      }
	    },
	    "/things": {
	      "post": {
	        "operationId": "createThing",
	        "summary": "Create a new thing",
	        "tags": ["beta"],
	        "responses": { "201": { "description": "Created" } }
	      }
	    }
	  }
	}`
	specPath := writeFile(t, tmpDir, "search-spec.json", specJSON)

	cfg := []indexing.SpecConfig{{
		DisplayName: "SearchSpec",
		Name:        "search-spec",
		File:        specPath,
		URL:         "",
	}}
	cfgBytes, err := json.Marshal(cfg)
	require.NoError(t, err)
	cfgPath := writeFile(t, tmpDir, "specs.json", string(cfgBytes))
	cachePath := filepath.Join(tmpDir, "cache.gob")
	reg, err := indexing.LoadConfigAndIndex(context.Background(), cfgPath, cachePath)
	require.NoError(t, err)

	// Build shards + alias
	idxDir := filepath.Join(tmpDir, "bleve_indexes")
	idx, err := indexing.BuildShardedIndices(idxDir, indexing.NewIndexMapping(), reg)
	require.NoError(t, err)

	// Search by operationId
	results, total, err := indexing.SearchBleve(idx, nil, nil, "getThing", 10, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), total)
	require.Len(t, results, 1)

	// Search by common term
	results, total, err = indexing.SearchBleve(idx, nil, nil, "thing", 10, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(2), total)
	require.Len(t, results, 2)

	// Search in summary
	results, total, err = indexing.SearchBleve(idx, nil, nil, "Retrieve", 10, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), total)
	require.Len(t, results, 1)

	// No hits
	results, total, err = indexing.SearchBleve(idx, nil, nil, "nonexistent", 10, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(0), total)
	require.Len(t, results, 0)
}
