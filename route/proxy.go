package route

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func WithCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func ProxyHandler(proxyMap map[string]string, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var target *url.URL
		var err error

		if u := r.URL.Query().Get("url"); u != "" {
			target, err = url.Parse(u)
			if err != nil {
				http.Error(w, "invalid target url: "+err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			// /api/{spec}/{path...}
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/"), "/", 2)
			if len(parts) < 2 {
				http.Error(w, "invalid API path", http.StatusBadRequest)
				return
			}
			base, ok := proxyMap[strings.ToLower(parts[0])]
			if !ok {
				http.Error(w, fmt.Sprintf("no proxyBase for %s", parts[0]), http.StatusBadRequest)
				return
			}
			baseURL, _ := url.Parse(base)
			rel := &url.URL{Path: path.Join(baseURL.Path, parts[1]), RawQuery: r.URL.RawQuery}
			target = baseURL.ResolveReference(rel)
		}

		req := r.Clone(r.Context())
		req.RequestURI = ""
		req.Host = target.Host
		req.URL = target

		for k, v := range r.Header {
			if h := strings.ToLower(k); h == "host" || h == "origin" || h == "referer" {
				continue
			}
			for _, vv := range v {
				req.Header.Add(k, vv)
			}
		}
		req.Header.Set("Origin", target.Scheme+"://"+target.Host)
		req.Header.Set("Referer", target.String())

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "error connecting to target: "+err.Error(), http.StatusBadGateway)
			return
		}
		log.Println("Proxying request to:", target.String())
		defer resp.Body.Close()

		for k, v := range resp.Header {
			if strings.ToLower(k) == "transfer-encoding" {
				continue
			}
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
