package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "database/sql"
    "strings"
    "time"
    "sync/atomic"

    "github.com/google/uuid"
    "github.com/joho/godotenv"

    _ "github.com/lib/pq"

    "github.com/davidw1457/chirpy/internal/database"
)

func main() {
    godotenv.Load()

    dbURL := os.Getenv("DB_URL")
    platform := os.Getenv("PLATFORM")


    db, err := sql.Open("postgres",  dbURL)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }

    dbQueries := database.New(db)

    mux := http.NewServeMux()

    server := http.Server{
        Handler: mux,
        Addr: ":8080",
    }

    cfg := apiConfig{qry: dbQueries, platform: platform}
    mux.Handle("/app/", cfg.middlewareMetricsInc(http.StripPrefix(
        "/app",
        http.FileServer(http.Dir("."),
    ))))
    mux.HandleFunc("GET /api/healthz", healthz)
    mux.HandleFunc("POST /api/chirps", cfg.postChirp)
    mux.HandleFunc("GET /api/chirps", cfg.getAllChirps)

    mux.HandleFunc("GET /admin/metrics", cfg.metrics)
    mux.HandleFunc("POST /admin/reset", cfg.reset)
    mux.HandleFunc("POST /api/users", cfg.addUser)

    server.ListenAndServe()
}

func healthz(rw http.ResponseWriter, rq *http.Request) {
    rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    _, err := rw.Write([]byte("OK"))
    if err != nil {
        fmt.Printf("healthz(%v, %v): %v\n", rw, rq, err)
    }
}

type apiConfig struct {
    fileserverHits atomic.Int32
    platform string
    qry *database.Queries
}

func (a *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
    return http.HandlerFunc(func (rw http.ResponseWriter, rq *http.Request) {
        a.fileserverHits.Add(1)
        next.ServeHTTP(rw, rq)
    })
}

func (a *apiConfig) metrics(rw http.ResponseWriter, rq *http.Request) {
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
        fmt.Printf("apiConfig.metrics(%v, %v): %v\n", rw, rq, err)
    }
}

func (a *apiConfig) reset(rw http.ResponseWriter, rq *http.Request) {
    if a.platform != "dev" {
        rw.WriteHeader(http.StatusForbidden)
        return
    }
    a.fileserverHits.Store(0)
    err := a.qry.ResetUsers(rq.Context())
    if err != nil {
        fmt.Printf("apiConfig.reset(%v, %v): %v\n", rw, rq, err)
        rw.WriteHeader(http.StatusInternalServerError)
        return
    }
    
    rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
    rw.WriteHeader(http.StatusOK)

    _, err = rw.Write([]byte("counter reset"))
    if err != nil {
        fmt.Printf("apiConfig.reset(%v, %v): %v\n", rw, rq, err)
    }
}

type chirp struct {
    Id uuid.UUID `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Body string `json:"body"`
    UserId  uuid.UUID `json:"user_id"`
}

func (a *apiConfig) postChirp(rw http.ResponseWriter, rq *http.Request) {
    type inputChirp struct {
        Body string `json:"body"`
        UserId uuid.UUID `json:"user_id"`
    }

    decoder := json.NewDecoder(rq.Body)
    chrp := inputChirp{}
    err := decoder.Decode(&chrp)
    if err != nil {
        fmt.Printf("validateChirp(%v, %v): %v\n", rw, rq, err)
        rw.WriteHeader(http.StatusInternalServerError)
        return
    }

    if chrp.Body == "" || len(chrp.UserId) == 0 {
        rw.WriteHeader(http.StatusBadRequest)
        return
    }

    chrp.Body = cleanString(chrp.Body)
    
    if len(chrp.Body) <= 140 {
        r, err := a.qry.CreateChirp(
            rq.Context(),
            database.CreateChirpParams{Body: chrp.Body, UserID: chrp.UserId},
        )
        if err != nil {
            rw.WriteHeader(http.StatusInternalServerError)
            return
        }

        respBody := chirp{
            Id: r.ID,
            CreatedAt: r.CreatedAt,
            UpdatedAt: r.UpdatedAt,
            Body: r.Body,
            UserId: r.UserID,
        }
        dat, err := json.Marshal(respBody)
        if err != nil {
            fmt.Printf("validateChirp(%v, %v): %v\n", rw, rq, err)
            rw.WriteHeader(http.StatusInternalServerError)
            return
        }
        rw.Header().Set("Content-Type", "application/json")
        rw.WriteHeader(http.StatusCreated)
        rw.Write(dat)
    } else {
        type response struct {
            Error string `json:"error"`
        }
        respBody := response{Error:"Chirp is too long"}
        dat, err := json.Marshal(respBody)
        if err != nil {
            fmt.Printf("validateChirp(%v, %v): %v\n", rw, rq, err)
            rw.WriteHeader(http.StatusInternalServerError)
            return
        }
        rw.Header().Set("Content-Type", "application/json")
        rw.WriteHeader(http.StatusBadRequest)
        rw.Write(dat)
    }
}

func cleanString(s string) string {
    badWords := []string{"kerfuffle", "sharbert", "fornax"}
    
    var cleaned string
    for _, b := range badWords {
        for _, w := range strings.Fields(s) {
            if strings.ToLower(w) == b {
                cleaned = fmt.Sprintf("%s ****", cleaned)
            } else {
                cleaned = fmt.Sprintf("%s %s", cleaned, w)
            }
        }
        s = cleaned
        cleaned = ""
    }
    return s[1:]
}

func (a *apiConfig) addUser(rw http.ResponseWriter, rq *http.Request) {
    type newUser struct {
        Email string `json:"email"`
    }

    decoder := json.NewDecoder(rq.Body)
    user := newUser{}
    err := decoder.Decode(&user)
    if err != nil {
        fmt.Printf("apiConfig.addUser(%v, %v): %v\n", rw, rq, err)
        rw.WriteHeader(http.StatusInternalServerError)
        return
    }

    if user.Email == "" {
        rw.WriteHeader(http.StatusBadRequest)
        return
    }

    r, err := a.qry.CreateUser(rq.Context(), user.Email)
    if err != nil {
        fmt.Printf("apiConfig.addUser(%v, %v): %v\n", rw, rq, err)
        rw.WriteHeader(http.StatusInternalServerError)
        return
    }

    type response struct {
        Id uuid.UUID `json:"id"`
        CreatedAt time.Time `json:"created_at"`
        UpdatedAt time.Time `json:"updated_at"`
        Email string `json:"email"`
    }
    respBody := response{
        Id: r.ID,
        CreatedAt: r.CreatedAt,
        UpdatedAt: r.UpdatedAt,
        Email: r.Email,
    }
    
    dat, err := json.Marshal(respBody)
    if err != nil {
        fmt.Printf("apiConfig.addUser(%v, %v): %v\n", rw, rq, err)
        rw.WriteHeader(http.StatusInternalServerError)
            return
    }
    rw.Header().Set("Content-Type", "application/json")
    rw.WriteHeader(http.StatusCreated)
    rw.Write(dat)
}

func (a *apiConfig) getAllChirps(rw http.ResponseWriter, rq *http.Request) {
    rows, err := a.qry.GetAllChirps(rq.Context())
    if err != nil {
        fmt.Printf("apiConfig.getAllChirps(%v, %v): %v\n", rw, rq, err)
        return
    }

    chirps := make([]chirp, len(rows))
    for i, r := range rows {
        chirps[i] = chirp{
            Id: r.ID,
            CreatedAt: r.CreatedAt,
            UpdatedAt: r.UpdatedAt,
            Body: r.Body,
            UserId: r.UserID,
        }
    }
    dat, err := json.Marshal(chirps)
    if err != nil {
        fmt.Printf("apiConfig.getAllChirps(%v, %v): %v\n", rw, rq, err)
        return
    }

    rw.Header().Set("Content-Type", "application/json")
    rw.WriteHeader(http.StatusOK)
    rw.Write(dat)
}
