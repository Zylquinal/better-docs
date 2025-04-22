package route

import (
	"better-docs/indexing"
	"better-docs/parser"
	"bufio"
	"bytes"
	"github.com/blevesearch/bleve/v2"
	"io"
	"net/http"
)

type ActionService struct {
	Registry indexing.Registry
	Index    bleve.Index
}

func NewActionService(reg indexing.Registry, idx bleve.Index) *ActionService {
	return &ActionService{
		Registry: reg,
		Index:    idx,
	}
}

func (s *ActionService) ActionHandler() http.HandlerFunc {
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

		_, _, _, err = indexing.FindOperation(s.Index, s.Registry, pr.Method, pr.URI)
		if err != nil {
			http.Error(w, "no match: "+err.Error(), http.StatusNotFound)
			return
		}

		response, err := parser.DoRequest(pr)
		if err != nil {
			http.Error(w, "request error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		responseString := parser.ResponseString(response)

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(responseString)); err != nil {
			http.Error(w, "failed to write response: "+err.Error(), http.StatusInternalServerError)
			return
		}

	}
}
