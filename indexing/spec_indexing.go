package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/getkin/kin-openapi/openapi3"
)

// SpecConfig describes an OpenAPI spec entry in the configuration JSON.
type SpecConfig struct {
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	File        string `json:"file"`
	URL         string `json:"url"`
}

// OpEntry maps an HTTP Method + path template to its OperationID and metadata.
type OpEntry struct {
	Method      string
	Template    string
	OperationID string
	Description string
	Tags        []string
}

// SpecIndex holds metadata needed at runtime for one spec.
type SpecIndex struct {
	SpecName    string
	File        string
	Host        string
	BasePath    string
	ContentHash string
}

// SpecBuild is an in-memory build artifact
type SpecBuild struct {
	SpecName string
	Host     string
	BasePath string
	Entries  []OpEntry
}

// Registry maps hostnames to their loaded SpecIndex.
type Registry map[string]*SpecIndex

type SearchResult struct {
	SpecName    string
	OperationID string
	Method      string
	Template    string
	Description string
	Tags        []string
}

type IndexMapping = *mapping.IndexMappingImpl

var (
	validVerbs = map[string]struct{}{ // allowed verbs under paths
		"get": {}, "put": {}, "post": {}, "delete": {},
		"options": {}, "head": {}, "patch": {},
		"trace": {}, "connect": {},
	}
	errNoServers = errors.New("spec has no servers[0] entry")
)

func computeSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readJSONFile(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// firstServerInfo extracts host and path from the first server URL.
func firstServerInfo(raw map[string]interface{}) (host, basePath string, err error) {
	sv, ok := raw["servers"].([]interface{})
	if !ok || len(sv) == 0 {
		return "", "", errNoServers
	}
	m, ok := sv[0].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("invalid servers[0]")
	}
	urlStr, ok := m["url"].(string)
	if !ok {
		return "", "", fmt.Errorf("servers[0].url not string")
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", "", err
	}
	host = u.Host
	basePath = strings.TrimRight(u.Path, "/")
	if basePath == "/" {
		basePath = ""
	}
	return host, basePath, nil
}

func LoadConfigAndIndex(ctx context.Context, configPath, cachePath string) (Registry, error) {
	var cfgs []SpecConfig
	if err := readJSONFile(configPath, &cfgs); err != nil {
		return nil, fmt.Errorf("reading config %q: %w", configPath, err)
	}

	old := make(map[string]string)
	if f, err := os.Open(cachePath); err == nil {
		_ = gob.NewDecoder(f).Decode(&old)
		_ = f.Close()
	}

	registry := make(Registry, len(cfgs))
	updated := make(map[string]string, len(cfgs))

	for _, cfg := range cfgs {
		abs, err := filepath.Abs(cfg.File)
		if err != nil {
			return nil, fmt.Errorf("resolving %q: %w", cfg.File, err)
		}
		rawBytes, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("reading spec %q: %w", cfg.Name, err)
		}
		hash := computeSHA(rawBytes)
		updated[cfg.Name] = hash

		var raw map[string]interface{}
		if err := json.Unmarshal(rawBytes, &raw); err != nil {
			return nil, fmt.Errorf("parsing spec %s: %w", cfg.Name, err)
		}
		host, base, err := firstServerInfo(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Name, err)
		}
		registry[host] = &SpecIndex{cfg.Name, abs, host, base, hash}
	}

	if f, err := os.Create(cachePath); err == nil {
		_ = gob.NewEncoder(f).Encode(updated)
		_ = f.Close()
	}

	return registry, nil
}

func NewIndexMapping() IndexMapping {
	im := mapping.NewIndexMapping()
	kw := bleve.NewTextFieldMapping()
	kw.Analyzer = keyword.Name
	im.DefaultMapping.AddFieldMappingsAt("SpecName", kw)
	im.DefaultMapping.AddFieldMappingsAt("Tags", kw)
	return im
}

// Unused, create an in-memory index
func BuildBleveIndex(im IndexMapping, builds []SpecBuild) (bleve.Index, error) {
	idx, err := bleve.NewMemOnly(im)
	if err != nil {
		return nil, err
	}
	for _, b := range builds {
		for _, e := range b.Entries {
			id := fmt.Sprintf("%s|%s|%s|%s", b.SpecName, e.Method, e.Template, e.OperationID)
			doc := map[string]interface{}{
				"SpecName":    b.SpecName,
				"OperationID": e.OperationID,
				"Method":      e.Method,
				"Template":    e.Template,
				"Description": e.Description,
				"Tags":        e.Tags,
			}
			if err := idx.Index(id, doc); err != nil {
				return nil, err
			}
		}
	}
	return idx, nil
}

func BuildShardedIndices(baseDir string, im IndexMapping, reg Registry) (bleve.Index, error) {
	alias := bleve.NewIndexAlias()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, spec := range reg {
		wg.Add(1)
		go func(spec *SpecIndex) {
			defer wg.Done()
			idx, err := BuildOrOpenSpecIndex(baseDir, im, spec)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			alias.Add(idx)
			mu.Unlock()
		}(spec)
	}

	wg.Wait()
	return alias, firstErr
}

// CheckIndexHealth tests that a match-all search succeeds.
func CheckIndexHealth(idx bleve.Index) error {
	_, err := idx.Search(bleve.NewSearchRequestOptions(bleve.NewMatchAllQuery(), 1, 0, false))
	return err
}

// BuildOrOpenSpecIndex creates or opens a disk-backed index for a spec.
func BuildOrOpenSpecIndex(baseDir string, im IndexMapping, spec *SpecIndex) (bleve.Index, error) {
	dir := filepath.Join(baseDir, spec.SpecName+".bleve")
	hashFile := filepath.Join(baseDir, spec.SpecName+".hash")

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}

	prevHashBytes, _ := os.ReadFile(hashFile)
	prevHash := string(prevHashBytes)

	_, dirErr := os.Stat(dir)

	needRebuild := prevHash != spec.ContentHash || os.IsNotExist(dirErr)
	if needRebuild {
		log.Printf("→ rebuilding %s (hash changed or missing)", spec.SpecName)
		_ = os.RemoveAll(dir)

		idx, err := bleve.NewUsing(
			dir,
			im,
			bleve.Config.DefaultIndexType,
			bleve.Config.DefaultKVStore,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("create index %q: %w", dir, err)
		}

		if err := indexSpecOnDisk(idx, spec); err != nil {
			idx.Close()
			return nil, err
		}

		if err := os.WriteFile(hashFile, []byte(spec.ContentHash), 0o644); err != nil {
			idx.Close()
			return nil, fmt.Errorf("write hash %q: %w", hashFile, err)
		}

		return idx, nil
	}

	idx, err := bleve.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open index %q: %w", dir, err)
	}
	return idx, nil
}

// FindOperation resolves a method+URL via Bleve, handling default ports.
func FindOperation(idx bleve.Index, reg Registry, method, rawURL string) (string, string, map[string]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	host := u.Hostname()
	meta, ok := reg[host]
	if !ok {
		return "", "", nil, fmt.Errorf("no spec for host %q", host)
	}

	base := strings.TrimRight(meta.BasePath, "/")
	full := u.Path
	rel := strings.TrimPrefix(full, base)
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	wantMethod := strings.ToUpper(method)

	log.Printf("FindOperation: broad‐search for %q in spec %q", rel, meta.SpecName)

	results, total, err := SearchBleve(
		idx,
		nil,
		nil,
		rel,
		100, 0,
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("search error: %w", err)
	}
	log.Printf("FindOperation: SearchBleve(rel) returned %d hits", total)

	for i, r := range results {
		log.Printf(
			"  candidate[%d]: spec=%q method=%q template=%q opID=%q",
			i, r.SpecName, r.Method, r.Template, r.OperationID,
		)
		if r.SpecName != meta.SpecName {
			continue
		}
		if r.Method != wantMethod {
			continue
		}
		if matchTemplate(r.Template, full) || matchTemplate(r.Template, rel) {
			pathVariable := extractPathParams(meta.BasePath, r.Template, full)
			log.Printf("FindOperation: matched candidate[%d] → %q", i, r.OperationID)
			return r.SpecName, r.OperationID, pathVariable, nil
		}
	}

	return "", "", nil, fmt.Errorf("no operation found for %s %s in spec %q", method, rel, meta.SpecName)
}

// SearchBleve performs a full-text search with optional filters and paging.
func SearchBleve(idx bleve.Index, specNames, tagFilters []string, queryStr string, limit, offset int) ([]SearchResult, uint64, error) {
	conj := []query.Query{bleve.NewQueryStringQuery(queryStr)}
	if len(specNames) > 0 {
		conj = append(conj, disjunction("SpecName", specNames))
	}
	if len(tagFilters) > 0 {
		conj = append(conj, disjunction("Tags", tagFilters))
	}
	sr := bleve.NewSearchRequestOptions(bleve.NewConjunctionQuery(conj...), limit, offset, false)
	sr.Fields = []string{"SpecName", "OperationID", "Method", "Template", "Description", "Tags"}
	res, err := idx.Search(sr)
	if err != nil {
		return nil, 0, err
	}
	out := make([]SearchResult, 0, len(res.Hits))
	for _, h := range res.Hits {
		tags := ifaceSliceToString(h.Fields["Tags"])
		parts := strings.Split(h.ID, "|")
		if len(parts) == 4 {
			out = append(out, SearchResult{parts[0], parts[3], parts[1], parts[2], "", tags})
		} else {
			out = append(out, SearchResult{Tags: tags})
		}
	}
	return out, res.Total, nil
}

// disjunction builds an OR query on a field for multiple values.
func disjunction(field string, vals []string) query.Query {
	terms := make([]query.Query, len(vals))
	for i, v := range vals {
		tq := bleve.NewTermQuery(v)
		tq.SetField(field)
		terms[i] = tq
	}
	return bleve.NewDisjunctionQuery(terms...)
}

// ifaceSliceToString normalizes interface{} to []string for Tags.
func ifaceSliceToString(in interface{}) []string {
	switch v := in.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, elem := range v {
			if s, ok := elem.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{v}
	default:
		return nil
	}
}

func indexSpecOnDisk(idx bleve.Index, spec *SpecIndex) error {
	data, err := os.ReadFile(spec.File)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	_ = json.Unmarshal(data, &raw)
	sanitizePaths(raw)
	sanitizeComponents(raw)
	injectMissingSchemas(raw)
	fixed, _ := json.Marshal(raw)

	doc, _ := openapi3.NewLoader().LoadFromData(fixed)
	for _, e := range extractOpEntries(doc) {
		docMap := map[string]interface{}{
			"SpecName":    spec.SpecName,
			"OperationID": e.OperationID,
			"Method":      e.Method,
			"Template":    e.Template,
			"Description": e.Description,
			"Tags":        e.Tags,
		}
		id := fmt.Sprintf("%s|%s|%s|%s", spec.SpecName, e.Method, e.Template, e.OperationID)
		if err := idx.Index(id, docMap); err != nil {
			return err
		}
	}

	return nil
}

// sanitizePaths normalizes HTTP verbs and removes unknown entries.
func sanitizePaths(raw map[string]interface{}) {
	paths, _ := raw["paths"].(map[string]interface{})
	for _, node := range paths {
		m, _ := node.(map[string]interface{})
		for k := range m {
			lk := strings.ToLower(k)
			if _, ok := validVerbs[lk]; ok {
				if lk != k {
					m[lk] = m[k]
					delete(m, k)
				}
				continue
			}
			if k == "parameters" {
				continue
			}
			delete(m, k)
		}
	}
}

// sanitizeComponents title-cases schema keys.
func sanitizeComponents(raw map[string]interface{}) {
	comps, _ := raw["components"].(map[string]interface{})
	schemas, _ := comps["schemas"].(map[string]interface{})
	for k, v := range schemas {
		title := strings.ToUpper(string(k[0])) + k[1:]
		if title != k {
			schemas[title] = v
		}
	}
}

// injectMissingSchemas adds stub schemas for missing $ref targets.
func injectMissingSchemas(raw map[string]interface{}) {
	refs := map[string]struct{}{}
	var walk func(interface{})
	walk = func(node interface{}) {
		switch n := node.(type) {
		case map[string]interface{}:
			for k, v := range n {
				if k == "$ref" {
					if s, ok := v.(string); ok {
						const p = "#/components/schemas/"
						if strings.HasPrefix(s, p) {
							refs[s[len(p):]] = struct{}{}
						}
					}
					continue
				}
				walk(v)
			}
		case []interface{}:
			for _, e := range n {
				walk(e)
			}
		}
	}
	walk(raw)
	comps, _ := raw["components"].(map[string]interface{})
	if comps == nil {
		comps = map[string]interface{}{}
		raw["components"] = comps
	}
	schemas, _ := comps["schemas"].(map[string]interface{})
	if schemas == nil {
		schemas = map[string]interface{}{}
		comps["schemas"] = schemas
	}
	for name := range refs {
		if _, exists := schemas[name]; !exists {
			schemas[name] = map[string]interface{}{"type": "object"}
		}
	}
}

// extractOpEntries collects OpEntry from an OpenAPI document.
func extractOpEntries(doc *openapi3.T) []OpEntry {
	var entries []OpEntry
	for tmpl, item := range doc.Paths.Map() {
		for method, op := range extractOperations(item) {
			desc := op.Summary
			if desc == "" {
				desc = op.Description
			}
			entries = append(entries, OpEntry{method, tmpl, op.OperationID, desc, op.Tags})
		}
	}
	return entries
}

// extractOperations maps PathItem fields to key/value verbs.
func extractOperations(item *openapi3.PathItem) map[string]*openapi3.Operation {
	ops := make(map[string]*openapi3.Operation)
	if item.Get != nil {
		ops["GET"] = item.Get
	}
	if item.Post != nil {
		ops["POST"] = item.Post
	}
	if item.Put != nil {
		ops["PUT"] = item.Put
	}
	if item.Delete != nil {
		ops["DELETE"] = item.Delete
	}
	if item.Options != nil {
		ops["OPTIONS"] = item.Options
	}
	if item.Head != nil {
		ops["HEAD"] = item.Head
	}
	if item.Patch != nil {
		ops["PATCH"] = item.Patch
	}
	if item.Trace != nil {
		ops["TRACE"] = item.Trace
	}
	if item.Connect != nil {
		ops["CONNECT"] = item.Connect
	}
	return ops
}

// matchTemplate checks path parameters against a template.
func matchTemplate(tmpl, path string) bool {
	ts := strings.Split(strings.Trim(tmpl, "/"), "/")
	ps := strings.Split(strings.Trim(path, "/"), "/")
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if strings.HasPrefix(ts[i], "{") && strings.HasSuffix(ts[i], "}") {
			continue
		}
		if ts[i] != ps[i] {
			return false
		}
	}
	return true
}

func extractPathParams(basePath, tmpl, fullPath string) map[string]string {
	clean := strings.TrimPrefix(fullPath, basePath)
	clean = strings.TrimPrefix(clean, "/")

	tSeg := strings.Split(strings.Trim(tmpl, "/"), "/")
	pSeg := strings.Split(strings.Trim(clean, "/"), "/")

	params := make(map[string]string, len(tSeg)/2)
	if len(tSeg) != len(pSeg) {
		log.Printf(
			"Path segments mismatch (template %d vs path %d): tmpl=%q  cleanPath=%q",
			len(tSeg), len(pSeg), tmpl, clean,
		)
		return params
	}

	for i, ts := range tSeg {
		if strings.HasPrefix(ts, "{") && strings.HasSuffix(ts, "}") {
			name := ts[1 : len(ts)-1]
			params[name] = pSeg[i]
		}
	}
	return params
}
