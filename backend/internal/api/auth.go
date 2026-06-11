package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password" binding:"required"`
}

// Login authenticates a user with username/email + password and returns a JWT token.
//
// @ID          login
// @Summary     Log in and obtain a bearer token
// @Description Authenticates a user by username or email plus password and returns a JWT bearer token. Public endpoint — no authentication required. Use the returned token as the Authorization: Bearer header for every other endpoint.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body body     loginRequest true "Login credentials (username or email, plus password)"
// @Success     200  {object} map[string]interface{} "object with the JWT token and the authenticated user"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Router      /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Accept either username or email as the identifier
	identifier := req.Username
	if identifier == "" {
		identifier = req.Email
	}
	if identifier == "" {
		respondError(c, http.StatusBadRequest, "username or email is required")
		return
	}

	var user models.User
	err := h.pool.QueryRow(c.Request.Context(),
		"SELECT id, username, email, password_hash, display_name FROM users WHERE username = $1 OR email = $1",
		identifier,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.DisplayName)
	if err == pgx.ErrNoRows {
		respondError(c, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal error")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		respondError(c, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, user.Email, user.DisplayName, h.cfg.JWTSecret)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not generate token")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"email":        user.Email,
			"display_name": user.DisplayName,
		},
	})
}

// Me returns the currently authenticated user's info from JWT claims.
//
// @ID          getCurrentUser
// @Summary     Get the current authenticated user
// @Description Returns the identity (id, username, email, display name) of the user owning the bearer token. Read-only. Useful to confirm who the token belongs to before taking actions.
// @Tags        auth
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with the authenticated user"
// @Failure     401 {object} map[string]string
// @Router      /auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":           claims.UserID,
			"username":     claims.Username,
			"email":        claims.Email,
			"display_name": claims.DisplayName,
		},
	})
}
