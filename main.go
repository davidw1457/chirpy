package main

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	polkaKey := os.Getenv("POLKA_KEY")

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

	cfg := apiConfig{
		qry:      dbQueries,
		platform: platform,
		secret:   secret,
		polkaKey: polkaKey,
	}
	mux.Handle("/app/", cfg.middlewareMetricsInc(http.StripPrefix(
		"/app",
		http.FileServer(http.Dir(".")))))

	mux.HandleFunc("DELETE /api/chirps/{chirpID}", cfg.deleteChirpsChirpID)

	mux.HandleFunc("GET /api/healthz", getHealthz)
	mux.HandleFunc("GET /api/chirps", cfg.getChirps)
	mux.HandleFunc("GET /admin/metrics", cfg.getMetrics)
	mux.HandleFunc("GET /api/chirps/{chirpID}", cfg.getChirpsChirpID)

	mux.HandleFunc("POST /api/chirps", cfg.postChirps)
	mux.HandleFunc("POST /admin/reset", cfg.postReset)
	mux.HandleFunc("POST /api/users", cfg.postUsers)
	mux.HandleFunc("POST /api/login", cfg.postLogin)
	mux.HandleFunc("POST /api/refresh", cfg.postRefresh)
	mux.HandleFunc("POST /api/revoke", cfg.postRevoke)
	mux.HandleFunc("POST /api/polka/webhooks", cfg.postPolkaWebhooks)

	mux.HandleFunc("PUT /api/users", cfg.putUsers)

	server.ListenAndServe()
}

func getHealthz(rw http.ResponseWriter, rq *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(http.StatusOK)

	_, err := rw.Write([]byte("OK"))
	if err != nil {
		fmt.Printf("getHealthz: %v\n", err)
	}
}

type apiConfig struct {
	fileserverHits atomic.Int32
	platform       string
	qry            *database.Queries
	secret         string
	polkaKey       string
}

func (a *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
		a.fileserverHits.Add(1)
		next.ServeHTTP(rw, rq)
	})
}

func (a *apiConfig) getMetrics(rw http.ResponseWriter, rq *http.Request) {
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
		fmt.Printf("apiConfig.getMetrics: %v\n", err)
	}
}

func (a *apiConfig) postReset(rw http.ResponseWriter, rq *http.Request) {
	if a.platform != "dev" {
		rw.WriteHeader(http.StatusForbidden)
		return
	}
	a.fileserverHits.Store(0)
	err := a.qry.ResetUsers(rq.Context())
	if err != nil {
		fmt.Printf("apiConfig.postReset: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(http.StatusOK)

	_, err = rw.Write([]byte("counter reset"))
	if err != nil {
		fmt.Printf("apiConfig.postReset: %v\n", err)
	}
}

type chirp struct {
	Id        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserId    uuid.UUID `json:"user_id"`
}

func (a *apiConfig) postChirps(rw http.ResponseWriter, rq *http.Request) {
	type inputChirp struct {
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(rq.Body)
	chrp := inputChirp{}
	err := decoder.Decode(&chrp)
	if err != nil {
		fmt.Printf("postChirps: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	tokenString, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("postChirps: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	userID, err := auth.ValidateJWT(tokenString, a.secret)
	if err != nil {
		fmt.Printf("postChirps: %v\n", err)
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
			fmt.Printf("postChirps: %v\n", err)
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
			fmt.Printf("postChirps: %v\n", err)
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

func (a *apiConfig) postUsers(rw http.ResponseWriter, rq *http.Request) {
	type input struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	decoder := json.NewDecoder(rq.Body)
	newUser := input{}
	err := decoder.Decode(&newUser)
	if err != nil {
		fmt.Printf("apiConfig.postUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if newUser.Email == "" || newUser.Password == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	newUser.Password, err = auth.HashPassword(newUser.Password)
	if err != nil {
		fmt.Printf("apiConfig.postUsers: %v\n", err)
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
		fmt.Printf("apiConfig.postUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	respBody := user{
		Id:          r.ID,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		Email:       r.Email,
		IsChirpyRed: r.IsChirpyRed,
	}

	dat, err := json.Marshal(respBody)
	if err != nil {
		fmt.Printf("apiConfig.postUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusCreated)
	rw.Write(dat)
}

func (a *apiConfig) getChirps(rw http.ResponseWriter, rq *http.Request) {
	authorID := rq.URL.Query().Get("author_id")

	var rows []database.Chirp
	var err error

	if authorID == "" {
		rows, err = a.qry.GetAllChirps(rq.Context())
	} else {
		userID, err := uuid.Parse(authorID)
		if err != nil {
			fmt.Printf("apiConfig.getChirps: %v\n", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rows, err = a.qry.GetChirpsByUserID(rq.Context(), userID)
	}
	if err != nil {
		fmt.Printf("apiConfig.getChirps: %v\n", err)
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
		fmt.Printf("apiConfig.getChirps: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

func (a *apiConfig) getChirpsChirpID(
	rw http.ResponseWriter,
	rq *http.Request,
) {
	id, err := uuid.Parse(rq.PathValue("chirpID"))
	if err != nil {
		fmt.Printf("apiConfig.getChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	row, err := a.qry.GetChirp(rq.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("apiConfig.getChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	} else if err != nil {
		fmt.Printf("apiConfig.getChirpsChripID: %v\n", err)
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
		fmt.Printf("apiConfig.getChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

type user struct {
	Id           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Email        string    `json:"email"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
}

func (a *apiConfig) postLogin(rw http.ResponseWriter, rq *http.Request) {
	type input struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	decoder := json.NewDecoder(rq.Body)
	inp := input{}
	err := decoder.Decode(&inp)
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	row, err := a.qry.GetUserByEmail(rq.Context(), inp.Email)
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := auth.CheckPasswordHash(
		inp.Password,
		row.HashedPassword,
	); err != nil {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusUnauthorized)
		rw.Write([]byte("Incorrect email or password"))
		return
	}

	tokenString, err := auth.MakeJWT(row.ID, a.secret, time.Hour)
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	refreshToken, err := auth.MakeRefreshToken()
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	_, err = a.qry.CreateRefreshToken(
		rq.Context(),
		database.CreateRefreshTokenParams{
			Token:  refreshToken,
			UserID: row.ID,
		},
	)
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	loggedInUser := user{
		Id:           row.ID,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Email:        row.Email,
		IsChirpyRed:  row.IsChirpyRed,
		Token:        tokenString,
		RefreshToken: refreshToken,
	}

	dat, err := json.Marshal(loggedInUser)
	if err != nil {
		fmt.Printf("apiConfig.postLogin: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

func (a *apiConfig) postRefresh(rw http.ResponseWriter, rq *http.Request) {
	refreshToken, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("apiConfig.postRefresh: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	refreshTokenRow, err := a.qry.GetRefreshToken(rq.Context(), refreshToken)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("apiConfig.postRefresh: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	} else if err != nil {
		fmt.Printf("apiConfig.postRefresh: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	tokenString, err := auth.MakeJWT(
		refreshTokenRow.UserID,
		a.secret,
		time.Hour,
	)
	if err != nil {
		fmt.Printf("apiConfig.postRefresh: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	type response struct {
		Token string `json:"token"`
	}
	tokenResp := response{Token: tokenString}

	dat, err := json.Marshal(tokenResp)
	if err != nil {
		fmt.Printf("apiConfig.postRefresh: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

func (a *apiConfig) postRevoke(rw http.ResponseWriter, rq *http.Request) {
	refreshToken, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("apiConfig.postRevoke: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	err = a.qry.RevokeRefreshToken(rq.Context(), refreshToken)
	if err != nil {
		fmt.Printf("apiConfig.postRevoke: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (a *apiConfig) putUsers(rw http.ResponseWriter, rq *http.Request) {
	tokenString, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	userID, err := auth.ValidateJWT(tokenString, a.secret)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	type input struct {
		Password string
		Email    string
	}

	decoder := json.NewDecoder(rq.Body)
	inp := input{}
	err = decoder.Decode(&inp)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	inp.Password, err = auth.HashPassword(inp.Password)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	userRow, err := a.qry.UpdateUser(
		rq.Context(),
		database.UpdateUserParams{
			Email:          inp.Email,
			HashedPassword: inp.Password,
			ID:             userID,
		},
	)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	respBody := user{
		Id:          userRow.ID,
		CreatedAt:   userRow.CreatedAt,
		UpdatedAt:   userRow.UpdatedAt,
		Email:       userRow.Email,
		IsChirpyRed: userRow.IsChirpyRed,
	}

	dat, err := json.Marshal(respBody)
	if err != nil {
		fmt.Printf("apiConfig.putUsers: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	rw.Write(dat)
}

func (a *apiConfig) deleteChirpsChirpID(
	rw http.ResponseWriter,
	rq *http.Request,
) {
	chirpID, err := uuid.Parse(rq.PathValue("chirpID"))
	if err != nil {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	chirp, err := a.qry.GetChirp(rq.Context(), chirpID)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	} else if err != nil {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	tokenString, err := auth.GetBearerToken(rq.Header)
	if err != nil {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	userID, err := auth.ValidateJWT(tokenString, a.secret)
	if err != nil {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	if chirp.UserID != userID {
		rw.WriteHeader(http.StatusForbidden)
		return
	}

	err = a.qry.DeleteChirp(rq.Context(), chirpID)
	if err != nil {
		fmt.Printf("apiConfig.deleteChirpsChirpID: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusNoContent)
}

func (a *apiConfig) postPolkaWebhooks(
	rw http.ResponseWriter,
	rq *http.Request,
) {
	apiKey, err := auth.GetAPIKey(rq.Header)
	if err != nil || apiKey != a.polkaKey {
		fmt.Printf("apiConfig.postPolkaWebhooks: %v\n", err)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}

	type input struct {
		Event string `json:"event"`
		Data  struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}

	decoder := json.NewDecoder(rq.Body)
	inp := input{}
	err = decoder.Decode(&inp)
	if err != nil {
		fmt.Printf("apiConfig.postPolkaWebhooks: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if inp.Event != "user.upgraded" {
		rw.WriteHeader(http.StatusNoContent)
		return
	}

	userID, err := uuid.Parse(inp.Data.UserID)
	if err != nil {
		fmt.Printf("apiConfig.postPolkaWebhooks: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	_, err = a.qry.UpdateToChirpyRed(rq.Context(), userID)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("apiConfig.postPolkaWebhooks: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	} else if err != nil {
		fmt.Printf("apiConfig.postPolkaWebhooks: %v\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusNoContent)
}
