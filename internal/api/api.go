package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shellhaki/openCBT/config"
	"github.com/shellhaki/openCBT/internal/auth"
	"github.com/shellhaki/openCBT/internal/httpx"
	"github.com/shellhaki/openCBT/internal/middleware"
	"github.com/shellhaki/openCBT/internal/models"
	"github.com/shellhaki/openCBT/internal/ws"
)

type Server struct {
	db       *pgxpool.Pool
	cfg      config.AppConfig
	hub      *ws.Hub
	upgrader websocket.Upgrader
}

func NewServer(db *pgxpool.Pool, cfg config.AppConfig) *Server {
	return &Server{
		db:  db,
		cfg: cfg,
		hub: ws.NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool {
				return true
			},
		},
	}
}

func (s *Server) StartExpiryWatcher(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.CloseExpiredRooms(ctx)
			}
		}
	}()
}

func (s *Server) Routes() *gin.Engine {
	r := gin.Default()
	_ = r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) {
		httpx.OK(c, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	api.POST("/auth/signup", s.signup)
	api.POST("/auth/login", s.login)

	protected := api.Group("")
	protected.Use(middleware.Auth(s.cfg.JWTSecret))

	teacher := protected.Group("/teacher")
	teacher.Use(middleware.RequireRole("teacher"))
	teacher.POST("/rooms", s.createRoom)
	teacher.GET("/rooms", s.listTeacherRooms)
	teacher.GET("/rooms/:room_id", s.getTeacherRoom)
	teacher.POST("/rooms/:room_id/start", s.startRoom)
	teacher.POST("/rooms/:room_id/close", s.closeRoom)

	student := protected.Group("/student")
	student.Use(middleware.RequireRole("student"))
	student.POST("/rooms/join", s.joinRoom)
	student.GET("/rooms/:room_id/exam", s.getStudentExam)
	student.POST("/rooms/:room_id/submit", s.submitExam)
	student.GET("/results", s.listStudentResults)

	r.GET("/ws/rooms/:room_id", s.roomSocket)

	return r
}

type signupRequest struct {
	FullName string `json:"full_name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required"`
	Age      *int   `json:"age"`
}

func (s *Server) signup(c *gin.Context) {
	var req signupRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.Role != "student" && req.Role != "teacher" {
		httpx.Error(c, http.StatusBadRequest, "role must be student or teacher")
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not hash password")
		return
	}

	var user models.User
	err = s.db.QueryRow(c.Request.Context(), `
		INSERT INTO users (full_name, email, password_hash, role, age)
		VALUES ($1, LOWER($2), $3, $4, $5)
		RETURNING id, full_name, email, role, age
	`, req.FullName, req.Email, passwordHash, req.Role, req.Age).
		Scan(&user.ID, &user.FullName, &user.Email, &user.Role, &user.Age)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_key") {
			httpx.Error(c, http.StatusConflict, "email already exists")
			return
		}
		httpx.Error(c, http.StatusInternalServerError, "could not create user")
		return
	}

	token, err := auth.CreateToken(user.ID, user.Role, s.cfg.JWTSecret, s.cfg.TokenTTL)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not create token")
		return
	}

	httpx.Created(c, models.AuthResponse{Token: token, User: user})
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (s *Server) login(c *gin.Context) {
	var req loginRequest
	if !bindJSON(c, &req) {
		return
	}

	var user models.User
	var passwordHash string
	err := s.db.QueryRow(c.Request.Context(), `
		SELECT id, full_name, email, password_hash, role, age
		FROM users
		WHERE email = LOWER($1)
	`, req.Email).Scan(&user.ID, &user.FullName, &user.Email, &passwordHash, &user.Role, &user.Age)
	if err != nil || !auth.CheckPassword(passwordHash, req.Password) {
		httpx.Error(c, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, err := auth.CreateToken(user.ID, user.Role, s.cfg.JWTSecret, s.cfg.TokenTTL)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not create token")
		return
	}

	httpx.OK(c, models.AuthResponse{Token: token, User: user})
}

func (s *Server) createRoom(c *gin.Context) {
	var req models.CreateRoomRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.DurationMinutes <= 0 {
		httpx.Error(c, http.StatusBadRequest, "duration_minutes must be greater than zero")
		return
	}
	if len(req.Questions) == 0 {
		httpx.Error(c, http.StatusBadRequest, "at least one question is required")
		return
	}

	ctx := c.Request.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not start room creation")
		return
	}
	defer rollback(ctx, tx)

	roomCode := makeRoomCode()
	passMark := req.PassMarkPercentage
	if passMark == 0 {
		passMark = 50
	}

	var room models.Room
	err = tx.QueryRow(ctx, `
		INSERT INTO exam_rooms
			(teacher_id, title, description, room_code, duration_minutes, show_results_to_students, pass_mark_percentage)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)
		RETURNING id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
	`, c.GetString(middleware.UserIDKey), req.Title, req.Description, roomCode, req.DurationMinutes, req.ShowResultsToStudents, passMark).
		Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description, &room.RoomCode, &room.DurationMinutes,
			&room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage, &room.StartsAt, &room.ClosesAt,
			&room.ClosedAt, &room.CreatedAt)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not create room")
		return
	}

	for questionIndex, question := range req.Questions {
		questionType := question.Type
		if questionType == "" {
			questionType = "objective"
		}
		points := question.Points
		if points == 0 {
			points = 1
		}

		var questionID string
		err = tx.QueryRow(ctx, `
			INSERT INTO questions (room_id, question_text, question_type, points, position)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, room.ID, question.Text, questionType, points, questionIndex+1).Scan(&questionID)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "could not create question")
			return
		}

		for optionIndex, option := range question.Options {
			_, err = tx.Exec(ctx, `
				INSERT INTO question_options (question_id, option_text, is_correct, position)
				VALUES ($1, $2, $3, $4)
			`, questionID, option.Text, option.IsCorrect, optionIndex+1)
			if err != nil {
				httpx.Error(c, http.StatusBadRequest, "could not create question option")
				return
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not save room")
		return
	}

	httpx.Created(c, room)
}

func (s *Server) listTeacherRooms(c *gin.Context) {
	rows, err := s.db.Query(c.Request.Context(), `
		SELECT id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
		FROM exam_rooms
		WHERE teacher_id = $1
		ORDER BY created_at DESC
	`, c.GetString(middleware.UserIDKey))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not list rooms")
		return
	}
	defer rows.Close()

	rooms, err := pgx.CollectRows(rows, pgx.RowToStructByName[models.Room])
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not read rooms")
		return
	}

	httpx.OK(c, rooms)
}

func (s *Server) getTeacherRoom(c *gin.Context) {
	room, err := s.getOwnedRoom(c.Request.Context(), c.Param("room_id"), c.GetString(middleware.UserIDKey))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "room not found")
		return
	}

	participants, err := s.roomParticipants(c.Request.Context(), room.ID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not load room participants")
		return
	}

	results, err := s.roomResults(c.Request.Context(), room.ID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not load room results")
		return
	}

	httpx.OK(c, gin.H{
		"room":         room,
		"participants": participants,
		"results":      results,
	})
}

func (s *Server) startRoom(c *gin.Context) {
	roomID := c.Param("room_id")
	teacherID := c.GetString(middleware.UserIDKey)
	startsAt := time.Now().UTC()

	var room models.Room
	err := s.db.QueryRow(c.Request.Context(), `
		UPDATE exam_rooms
		SET status = 'active', starts_at = COALESCE(starts_at, $3),
			closes_at = COALESCE(closes_at, $3 + (duration_minutes || ' minutes')::interval)
		WHERE id = $1 AND teacher_id = $2 AND status IN ('waiting', 'active')
		RETURNING id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
	`, roomID, teacherID, startsAt).Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description,
		&room.RoomCode, &room.DurationMinutes, &room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage,
		&room.StartsAt, &room.ClosesAt, &room.ClosedAt, &room.CreatedAt)
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "room not found or cannot be started")
		return
	}

	s.hub.Broadcast(room.ID, "room_started", room)
	httpx.OK(c, room)
}

func (s *Server) closeRoom(c *gin.Context) {
	room, err := s.closeOwnedRoom(c.Request.Context(), c.Param("room_id"), c.GetString(middleware.UserIDKey))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "room not found or already closed")
		return
	}

	s.hub.CloseRoom(room.ID)
	httpx.OK(c, room)
}

type joinRoomRequest struct {
	RoomCode string `json:"room_code" binding:"required"`
}

func (s *Server) joinRoom(c *gin.Context) {
	var req joinRoomRequest
	if !bindJSON(c, &req) {
		return
	}

	var room models.Room
	err := s.db.QueryRow(c.Request.Context(), `
		SELECT id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
		FROM exam_rooms
		WHERE room_code = UPPER($1) AND status IN ('waiting', 'active')
	`, req.RoomCode).Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description, &room.RoomCode,
		&room.DurationMinutes, &room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage,
		&room.StartsAt, &room.ClosesAt, &room.ClosedAt, &room.CreatedAt)
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "room not found or closed")
		return
	}

	_, err = s.db.Exec(c.Request.Context(), `
		INSERT INTO room_participants (room_id, student_id)
		VALUES ($1, $2)
		ON CONFLICT (room_id, student_id)
		DO UPDATE SET status = 'joined'
	`, room.ID, c.GetString(middleware.UserIDKey))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not join room")
		return
	}

	httpx.OK(c, room)
}

func (s *Server) getStudentExam(c *gin.Context) {
	roomID := c.Param("room_id")
	studentID := c.GetString(middleware.UserIDKey)

	if !s.studentJoinedRoom(c.Request.Context(), roomID, studentID) {
		httpx.Error(c, http.StatusForbidden, "join the room before opening the exam")
		return
	}

	room, questions, err := s.publicExam(c.Request.Context(), roomID)
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "exam not found")
		return
	}
	if room.Status == "closed" || room.Status == "cancelled" {
		httpx.Error(c, http.StatusForbidden, "exam is closed")
		return
	}

	httpx.OK(c, gin.H{"room": room, "questions": questions})
}

func (s *Server) submitExam(c *gin.Context) {
	var req models.SubmitRequest
	if !bindJSON(c, &req) {
		return
	}

	result, err := s.calculateSubmission(c.Request.Context(), c.Param("room_id"), c.GetString(middleware.UserIDKey), req.Answers)
	if err != nil {
		switch {
		case errors.Is(err, errRoomClosed):
			httpx.Error(c, http.StatusForbidden, "room is closed")
		case errors.Is(err, errNotParticipant):
			httpx.Error(c, http.StatusForbidden, "join the room before submitting")
		default:
			httpx.Error(c, http.StatusInternalServerError, "could not submit exam")
		}
		return
	}

	httpx.OK(c, result)
}

func (s *Server) listStudentResults(c *gin.Context) {
	rows, err := s.db.Query(c.Request.Context(), `
		SELECT r.id, r.room_id, r.student_id, r.submission_id, r.score, r.total_points, r.percentage,
			COALESCE(r.grade, '') AS grade, r.passed, r.calculated_at
		FROM results r
		JOIN exam_rooms er ON er.id = r.room_id
		WHERE r.student_id = $1 AND er.show_results_to_students = TRUE
		ORDER BY r.calculated_at DESC
	`, c.GetString(middleware.UserIDKey))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not list results")
		return
	}
	defer rows.Close()

	results, err := pgx.CollectRows(rows, pgx.RowToStructByName[models.Result])
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "could not read results")
		return
	}

	httpx.OK(c, results)
}

func (s *Server) roomSocket(c *gin.Context) {
	token := c.Query("token")
	claims, err := auth.ParseToken(token, s.cfg.JWTSecret)
	if err != nil {
		httpx.Error(c, http.StatusUnauthorized, "invalid token")
		return
	}

	roomID := c.Param("room_id")
	if claims.Role == "student" && !s.studentJoinedRoom(c.Request.Context(), roomID, claims.UserID) {
		httpx.Error(c, http.StatusForbidden, "join the room before connecting")
		return
	}

	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	s.hub.Add(roomID, conn)
	defer s.hub.Remove(roomID, conn)
	defer conn.Close()
	defer s.markDisconnected(roomID, claims.UserID, claims.Role)

	for {
		if _, _, err := conn.NextReader(); err != nil {
			return
		}
	}
}

func bindJSON(c *gin.Context, out any) bool {
	if err := c.ShouldBindJSON(out); err != nil {
		httpx.Error(c, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func rollback(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

func makeRoomCode() string {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return strings.ToUpper(hex.EncodeToString([]byte(time.Now().Format("150405"))))
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}
