package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	_ "github.com/lib/pq"

	"github.com/davidw1457/chirpy/internal/auth"
	"github.com/davidw1457/chirpy/internal/database"
)

func main() {
	godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	secret := os.Getenv("SECRET")

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	dbQueries := database.New(db)

	mux := http.NewServeMux()

	server := http.Server{
		Handler: mux,
		Addr:    ":8080",
	}

	cfg := apiConfig{qry: dbQueries, platform: platform, secret: secret}
	mux.Handle("/app/", cfg.middlewareMetricsInc(http.StripPrefix(
		"/app",
		http.FileServer(http.Dir(".")))))

	mux.HandleFunc("GET /api/healthz", healthz)
	mux.HandleFunc("GET /api/chirps", cfg.getAllChirps)
	mux.HandleFunc("GET /admin/metrics", cfg.metrics)
	mux.HandleFunc("GET /api/chirps/{chirpID}", cfg.getChirp)
	mux.HandleFunc("POST /api/chirps", cfg.postChirp)
	mux.HandleFunc("POST /admin/reset", cfg.reset)
	mux.HandleFunc("POST /api/users", cfg.addUser)
	mux.HandleFunc("POST /api/login", cfg.login)

	server.ListenAndServe()
}

func healthz(rw http.ResponseWriter, rq *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(http.StatusOK)

	_, err := rw.Write([]byte("OK"))
	if err != nil {
		fmt.Printf("healthz: %v\n", err)
	}
}

type apiConfig struct {
	fileserverHits atomic.Int32
	platform       string
	qry            *database.Queries
	secret         string
}

func (a *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
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
		fmt.Printf("apiConfig.metrics: %v\n", err)
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
		fmt.Printf("apiConfig.reset: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(http.StatusOK)

	_, err = rw.Write([]byte("counter reset"))
	if err != nil {
		fmt.Printf("apiConfig.reset: %v\n", err)
	}
}

type chirp struct {
	Id        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserId    uuid.UUID `json:"user_id"`
}

func (a *apiConfig) postChirp(rw http.ResponseWriter, rq *http.Request) {
	type inputChirp struct {
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(rq.Body)
	chrp := inputChirp{}
	err := decoder.Decode(&chrp)
	if err != nil {
		fmt.Printf("postChirp: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	tokenString, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("postChirp: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	userID, err := auth.ValidateJWT(tokenString, a.secret)
	if err != nil {
		fmt.Printf("postChirp: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	if chrp.Body == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	chrp.Body = cleanString(chrp.Body)

	if len(chrp.Body) <= 140 {
		r, err := a.qry.CreateChirp(
			rq.Context(),
			database.CreateChirpParams{Body: chrp.Body, UserID: userID},
		)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		respBody := chirp{
			Id:        r.ID,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			Body:      r.Body,
			UserId:    r.UserID,
		}
		dat, err := json.Marshal(respBody)
		if err != nil {
			fmt.Printf("postChirp: %v\n", err)
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
		respBody := response{Error: "Chirp is too long"}
		dat, err := json.Marshal(respBody)
		if err != nil {
			fmt.Printf("postChirp: %v\n", err)
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
	type input struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	decoder := json.NewDecoder(rq.Body)
	newUser := input{}
	err := decoder.Decode(&newUser)
	if err != nil {
		fmt.Printf("apiConfig.addUser: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if newUser.Email == "" || newUser.Password == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	newUser.Password, err = auth.HashPassword(newUser.Password)
	if err != nil {
		fmt.Printf("apiConfig.addUser: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	r, err := a.qry.CreateUser(
		rq.Context(),
		database.CreateUserParams{
			Email:          newUser.Email,
			HashedPassword: newUser.Password,
		},
	)
	if err != nil {
		fmt.Printf("apiConfig.addUser: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	respBody := user{
		Id:        r.ID,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		Email:     r.Email,
	}

	dat, err := json.Marshal(respBody)
	if err != nil {
		fmt.Printf("apiConfig.addUser: %v\n", err)
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
		fmt.Printf("apiConfig.getAllChirps: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	chirps := make([]chirp, len(rows))
	for i, r := range rows {
		chirps[i] = chirp{
			Id:        r.ID,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			Body:      r.Body,
			UserId:    r.UserID,
		}
	}
	dat, err := json.Marshal(chirps)
	if err != nil {
		fmt.Printf("apiConfig.getAllChirps: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

func (a *apiConfig) getChirp(rw http.ResponseWriter, rq *http.Request) {
	id, err := uuid.Parse(rq.PathValue("chirpID"))
	if err != nil {
		fmt.Printf("apiConfig.getChirp: %v\n", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	row, err := a.qry.GetChirp(rq.Context(), id)
	if err == sql.ErrNoRows {
		rw.WriteHeader(http.StatusNotFound)
		return
	} else if err != nil {
		fmt.Printf("apiConfig.getChirp: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	chrp := chirp{
		Id:        row.ID,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Body:      row.Body,
		UserId:    row.UserID,
	}

	dat, err := json.Marshal(chrp)
	if err != nil {
		fmt.Printf("apiConfig.GetChirp: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

type user struct {
	Id        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	Token     string    `json:"token"`
}

func (a *apiConfig) login(rw http.ResponseWriter, rq *http.Request) {
	type input struct {
		Password         string `json:"password"`
		Email            string `json:"email"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
	}

	decoder := json.NewDecoder(rq.Body)
	inp := input{}
	err := decoder.Decode(&inp)
	if err != nil {
		fmt.Printf("apiConfig.login: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	row, err := a.qry.GetUserByEmail(rq.Context(), inp.Email)
	if err != nil {
		fmt.Printf("apiConfig.login: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := auth.CheckPasswordHash(inp.Password, row.HashedPassword); err != nil {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusUnauthorized)
		rw.Write([]byte("Incorrect email or password"))
		return
	}

	expiration := 3600 * time.Second
	if inp.ExpiresInSeconds > 0 && inp.ExpiresInSeconds < 3600 {
		expiration = time.Duration(inp.ExpiresInSeconds) * time.Second
	}

	tokenString, err := auth.MakeJWT(row.ID, a.secret, expiration)
	if err != nil {
		fmt.Printf("apiConfig.login: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	loggedInUser := user{
		Id:        row.ID,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Email:     row.Email,
		Token:     tokenString,
	}

	dat, err := json.Marshal(loggedInUser)
	if err != nil {
		fmt.Printf("apiConfig.login: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}
