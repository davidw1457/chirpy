package main

import (
    "fmt"
    "net/http"
    "sync/atomic"
)

func main() {
    mux := http.NewServeMux()

    server := http.Server{
        Handler: mux,
        Addr: ":8080",
    }

    cfg := apiConfig{}
    mux.Handle("/app/", cfg.middlewareMetricsInc(http.StripPrefix(
        "/app",
        http.FileServer(http.Dir("."),
    ))))
    mux.HandleFunc("GET /api/healthz", healthz)
    mux.HandleFunc("GET /admin/metrics", cfg.metrics)
    mux.HandleFunc("POST /admin/reset", cfg.reset)

    server.ListenAndServe()
}

func healthz(rw http.ResponseWriter, _ *http.Request) {
    rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    _, err := rw.Write([]byte("OK"))
    if err != nil {
        fmt.Println(err)
    }
}

type apiConfig struct {
    fileserverHits atomic.Int32
}

func (a *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
    return http.HandlerFunc(func (rw http.ResponseWriter, rq *http.Request) {
        a.fileserverHits.Add(1)
        next.ServeHTTP(rw, rq)
    })
}

func (a *apiConfig) metrics(rw http.ResponseWriter, _ *http.Request) {
    rw.Header().Set("Content-Type", "text/html; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    ht := fmt.Sprintf(`
<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, a.fileserverHits.Load())
    _, err := rw.Write([]byte(ht))
    if err != nil {
        fmt.Println(err)
    }
}

func (a *apiConfig) reset(rw http.ResponseWriter, _ *http.Request) {
    a.fileserverHits.Store(0)
    
    rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    _, err := rw.Write([]byte("counter reset"))
    if err != nil {
        fmt.Println(err)
    }
}

