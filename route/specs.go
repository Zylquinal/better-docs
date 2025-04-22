package route

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

type Spec struct {
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	File        string `json:"file"`
	URL         string `json:"url"`
	ProxyBase   string `json:"proxyBase"`
}

func LoadSpecs(path string) ([]Spec, map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open specs file: %w", err)
	}
	defer f.Close()

	var specs []Spec
	if err := json.NewDecoder(f).Decode(&specs); err != nil {
		return nil, nil, fmt.Errorf("decode specs: %w", err)
	}
	pm := make(map[string]string, len(specs))
	for _, s := range specs {
		pm[strings.ToLower(s.Name)] = strings.TrimRight(s.ProxyBase, "/")
	}
	return specs, pm, nil
}

func SpecsHandler(specs []Spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(specs); err != nil {
			log.Printf("encode specs: %v", err)
		}
	}
}

func SpecByIDHandler(specs []Spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(r.URL.Path[len("/api/specs/"):], "/")
		if id == "" {
			http.Error(w, "spec id not provided", http.StatusBadRequest)
			return
		}
		for _, s := range specs {
			if s.Name == id {
				ctype := "application/json"
				if strings.HasSuffix(s.File, ".yaml") || strings.HasSuffix(s.File, ".yml") {
					ctype = "application/x-yaml"
				}
				w.Header().Set("Content-Type", ctype)
				http.ServeFile(w, r, s.File)
				return
			}
		}
		http.Error(w, "spec not found", http.StatusNotFound)
	}
}
