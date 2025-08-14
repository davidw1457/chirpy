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
    mux.HandleFunc("/healthz", healthCheck)
    mux.HandleFunc("/metrics", cfg.hitCounter)
    mux.HandleFunc("/reset", cfg.reset)

    server.ListenAndServe()
}

func healthCheck(rw http.ResponseWriter, _ *http.Request) {
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

func (a *apiConfig) hitCounter(rw http.ResponseWriter, _ *http.Request) {
    rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    count := fmt.Sprintf("Hits: %d", a.fileserverHits.Load())
    _, err := rw.Write([]byte(count))
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

