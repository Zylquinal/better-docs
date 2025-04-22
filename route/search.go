package route

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"better-docs/indexing"
	"better-docs/parser"
	"github.com/blevesearch/bleve/v2"
)

type SearchService struct {
	Registry indexing.Registry
	Index    bleve.Index
}

func NewSearchService(registry indexing.Registry, idx bleve.Index) *SearchService {
	return &SearchService{
		Registry: registry,
		Index:    idx,
	}
}

// SearchHandler returns an HTTP handler for full-text and filtered searches.
func (s *SearchService) SearchHandler() http.HandlerFunc {
	type response struct {
		Total   int                     `json:"total"`
		Results []indexing.SearchResult `json:"results"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		specNames := r.URL.Query()["spec"]
		tagFilters := r.URL.Query()["tag"]
		limit, offset := 20, 0
		if ls := r.URL.Query().Get("limit"); ls != "" {
			if n, err := strconv.Atoi(ls); err == nil && n > 0 {
				limit = n
			}
		}
		if os := r.URL.Query().Get("offset"); os != "" {
			if n, err := strconv.Atoi(os); err == nil && n >= 0 {
				offset = n
			}
		}

		results, total, err := indexing.SearchBleve(s.Index, specNames, tagFilters, q, limit, offset)
		if err != nil {
			http.Error(w, "search error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := response{Total: int(total), Results: results}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "failed to write response: "+err.Error(), http.StatusInternalServerError)
		}
	}
}

// RaSearchHandler RestAssured log lookups.
func (s *SearchService) RaSearchHandler() http.HandlerFunc {

	type ParsedRequest = parser.ParsedRequest

	type Response struct {
		SpecName    string        `json:"specName"`
		OperationId string        `json:"operationId"`
		ParsedInfo  ParsedRequest `json:"parsedInfo"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		pr, err := parser.ParseLog(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
			return
		}

		log.Println("Parsed Request:", pr.Method, pr.URI)

		specName, opID, pathParams, err := indexing.FindOperation(s.Index, s.Registry, pr.Method, pr.URI)
		if err != nil {
			http.Error(w, "no match: "+err.Error(), http.StatusNotFound)
			return
		}

		pr.PathParams = pathParams

		log.Printf("Path Params: %v", pathParams)

		w.Header().Set("Content-Type", "application/json")

		response := Response{
			SpecName:    specName,
			OperationId: opID,
			ParsedInfo:  pr,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "failed to write response: "+err.Error(), http.StatusInternalServerError)
		}
	}
}
