package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultSpecsPath   = "specs.json"
	defaultProxyPrefix = "http://localhost:5001/api"
	httpTimeout        = 30 * time.Second
	filePerm           = 0o644
)

var (
	specsPath   string
	proxyPrefix string
	namesFilter string

	httpClient = &http.Client{Timeout: httpTimeout}
)

var replacementParameters = []map[string]interface{}{
	{"name": "storeId", "in": "query", "required": true, "example": "storeId"},
	{"name": "channelId", "in": "query", "required": true, "example": "channelId"},
	{"name": "clientId", "in": "query", "required": true, "example": "clientId"},
	{"name": "username", "in": "query", "required": true, "example": "username"},
	{"name": "requestId", "in": "query", "required": true, "example": "requestId"},
}

type Spec struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"displayName"`
	File                  string   `json:"file"`
	URL                   string   `json:"url"`
	OverrideRemoveDefault bool     `json:"overrideRemoveDefault"`
	OverrideServers       []Server `json:"overrideServers"`
}

type Server struct {
	Description string `json:"description"`
	URL         string `json:"url"`
}

func loadSpecs(path string) ([]Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var specs []Spec
	return specs, json.Unmarshal(b, &specs)
}

func saveSpecs(path string, specs []Spec) error {
	b, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, filePerm)
}

func writeSpec(dst string, spec map[string]interface{}) error {
	var (
		out []byte
		err error
	)
	switch strings.ToLower(filepath.Ext(dst)) {
	case ".yml", ".yaml":
		out, err = yaml.Marshal(spec)
	default:
		out, err = json.MarshalIndent(spec, "", "  ")
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, out, filePerm)
}

func download(u string) ([]byte, error) {
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func parseSpec(b []byte) (map[string]interface{}, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err == nil {
		return m, nil
	}
	return m, yaml.Unmarshal(b, &m)
}

func convertSwaggerToOpenAPI(raw []byte, toFormat string) ([]byte, error) {
	inFile, err := os.CreateTemp("", "swagger-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(inFile.Name()) // clean up

	if _, err = inFile.Write(raw); err != nil {
		inFile.Close()
		return nil, err
	}
	inFile.Close()

	outFile := inFile.Name() + ".out"
	cmd := exec.Command("java", "-jar", "swagger-convert.jar",
		"-i", inFile.Name(), "-o", outFile, "-f", toFormat)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("converter: %v: %s", err, stderr.String())
	}
	defer os.Remove(outFile)

	return os.ReadFile(outFile)
}

func sanitizeServers(openapi map[string]interface{}) {
	servers, ok := openapi["servers"].([]interface{})
	if !ok {
		return
	}
	for i, s := range servers {
		if sv, ok := s.(map[string]interface{}); ok {
			if urlStr, ok := sv["url"].(string); ok && strings.HasPrefix(urlStr, "//") {
				sv["url"] = "http:" + urlStr
				servers[i] = sv
			}
		}
	}
	openapi["servers"] = servers
}

func replaceMandatoryParameters(openapi map[string]interface{}) {
	paths, ok := openapi["paths"].(map[string]interface{})
	if !ok {
		return
	}

	for _, v := range paths { // verb group
		if methods, ok := v.(map[string]interface{}); ok {
			for _, d := range methods { // method detail
				detail, ok := d.(map[string]interface{})
				if !ok {
					continue
				}

				params, ok := detail["parameters"].([]interface{})
				if !ok {
					continue
				}

				var newParams []interface{}
				for _, p := range params {
					if pm, ok := p.(map[string]interface{}); ok &&
						pm["in"] == "query" &&
						(pm["name"] == "mandatoryParameter" || pm["name"] == "mandatoryParam") {

						// replace with fixed list
						for _, rp := range replacementParameters {
							newParams = append(newParams, rp)
						}
						continue
					}
					newParams = append(newParams, p)
				}
				detail["parameters"] = newParams
			}
		}
	}
}

func buildFilterSet(names string) map[string]struct{} {
	if names == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, n := range strings.Split(names, ",") {
		set[strings.TrimSpace(n)] = struct{}{}
	}
	return set
}

func firstServerURL(raw map[string]interface{}) string {
	if svs, ok := raw["servers"].([]interface{}); ok && len(svs) > 0 {
		if m, ok := svs[0].(map[string]interface{}); ok {
			if urlStr, _ := m["url"].(string); urlStr != "" {
				return urlStr
			}
		}
	}
	return ""
}

func fetchReferencedServerURL(specs []Spec, refName string) (string, string) {
	for i := range specs {
		if specs[i].Name != refName {
			continue
		}
		refSpec := &specs[i]
		b, err := download(refSpec.URL)
		if err != nil {
			log.Printf("⚠️ download referenced spec %s: %v", refName, err)
			return "", ""
		}
		raw, _ := parseSpec(b)
		if raw["swagger"] == "2.0" {
			format := "json"
			ext := strings.ToLower(filepath.Ext(refSpec.File))
			if ext == ".yml" || ext == ".yaml" {
				format = "yaml"
			}
			if conv, err := convertSwaggerToOpenAPI(b, format); err == nil {
				raw, _ = parseSpec(conv)
			}
		}
		sanitizeServers(raw)
		return refSpec.DisplayName, firstServerURL(raw)
	}
	log.Printf("⚠️ referenced spec %s not found", refName)
	return "", ""
}

func applyServerOverrides(raw map[string]interface{}, sp Spec, fullSpecs []Spec) {
	var servers []interface{}
	if existing, ok := raw["servers"].([]interface{}); ok && !sp.OverrideRemoveDefault {
		servers = existing

		if len(servers) > 0 {
			if m, ok := servers[0].(map[string]interface{}); ok {
				if _, ok := m["description"]; ok {
					m["description"] = sp.DisplayName
				}
			}
		}
	}

	for _, o := range sp.OverrideServers {
		desc, url := o.Description, o.URL

		// resolve ref::NAME
		if strings.HasPrefix(o.Description, "ref::") || strings.HasPrefix(o.URL, "ref::") {
			refName := strings.TrimPrefix(strings.TrimPrefix(o.Description, "ref::"), "ref::")
			if refName == "" {
				refName = strings.TrimPrefix(o.URL, "ref::")
			}
			refDesc, refURL := fetchReferencedServerURL(fullSpecs, refName)
			if refDesc != "" {
				desc = refDesc
			}
			if refURL != "" {
				url = refURL
			}
		}

		replaced := false
		for i, sv := range servers {
			if m, ok := sv.(map[string]interface{}); ok {
				if m["description"] == desc {
					servers[i] = map[string]interface{}{"description": desc, "url": url}
					replaced = true
					break
				}
			}
		}
		if !replaced {
			servers = append(servers, map[string]interface{}{"description": desc, "url": url})
		}
	}

	raw["servers"] = servers
}

func processSpec(sp Spec, allSpecs []Spec) Spec {
	log.Printf("processing %s from %s …", sp.Name, sp.URL)

	content, err := download(sp.URL)
	if err != nil {
		log.Printf("download failed: %v", err)
		return sp
	}

	// legacy <Json> wrapper (sometimes downloaded specs may have this)
	contentStr := strings.TrimSpace(string(content))
	if strings.HasPrefix(contentStr, "<Json>") {
		content = []byte(strings.TrimSuffix(strings.TrimPrefix(contentStr, "<Json>"), "</Json>"))
	}

	raw, err := parseSpec(content)
	if err != nil {
		log.Printf("parse error: %v", err)
		_ = os.WriteFile(sp.File, content, filePerm)
		return sp
	}

	if raw["swagger"] == "2.0" {
		format := "json"
		if ext := strings.ToLower(filepath.Ext(sp.File)); ext == ".yml" || ext == ".yaml" {
			format = "yaml"
		}
		if conv, err := convertSwaggerToOpenAPI(content, format); err == nil {
			raw, _ = parseSpec(conv)
		} else {
			log.Printf("conversion failed: %v", err)
		}
	}

	sanitizeServers(raw)
	replaceMandatoryParameters(raw)
	applyServerOverrides(raw, sp, allSpecs)

	if err := writeSpec(sp.File, raw); err != nil {
		log.Printf("write %s: %v", sp.File, err)
	}
	time.Sleep(100 * time.Millisecond) // gentle pacing
	return sp
}

var rootCmd = &cobra.Command{
	Use:   "swaggerctl",
	Short: "CLI for converting and updating Swagger/OpenAPI specs",
	Run:   func(_ *cobra.Command, _ []string) { execute() },
}

func init() {
	rootCmd.Flags().StringVarP(&specsPath, "specs", "s", defaultSpecsPath, "path to specs JSON file")
	rootCmd.Flags().StringVarP(&proxyPrefix, "proxy", "p", defaultProxyPrefix, "proxy URL prefix (currently unused)")
	rootCmd.Flags().StringVarP(&namesFilter, "names", "n", "", "comma‑separated spec names to process (default all)")
}

func execute() {
	specs, err := loadSpecs(specsPath)
	if err != nil {
		log.Fatalf("load %s: %v", specsPath, err)
	}

	filter := buildFilterSet(namesFilter)
	var updated []Spec

	for _, sp := range specs {
		if filter != nil {
			if _, ok := filter[sp.Name]; !ok {
				continue
			}
		}
		updated = append(updated, processSpec(sp, specs))
	}

	if err := saveSpecs(specsPath, updated); err != nil {
		log.Fatalf("update %s: %v", specsPath, err)
	}
	log.Println("✅ specs JSON updated")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
