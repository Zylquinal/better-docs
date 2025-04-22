package route

import (
	"net/http"
)

func StaticFileHandler(dir string) http.Handler {
	return http.StripPrefix("/static/", http.FileServer(http.Dir(dir)))
}

func IndexHandler(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, file)
	}
}

func RegisterRoutes(
	mux *http.ServeMux,
	specs []Spec,
	proxyMap map[string]string,
	client *http.Client,
	staticDir, indexFile string,
	searchSvc *SearchService,
	actionSvc *ActionService,
) {
	mux.HandleFunc("/api/specs", SpecsHandler(specs))
	mux.HandleFunc("/api/specs/", SpecByIDHandler(specs))

	proxy := WithCORS(ProxyHandler(proxyMap, client))
	mux.HandleFunc("/api", proxy)
	mux.HandleFunc("/api/", proxy)

	mux.Handle("/static/", StaticFileHandler(staticDir))
	mux.HandleFunc("/", IndexHandler(indexFile))

	mux.Handle("/search", searchSvc.SearchHandler())
	mux.Handle("/raSearch", searchSvc.RaSearchHandler())
	mux.Handle("/action", actionSvc.ActionHandler())
}
