package models

import "time"

type User struct {
	ID       string `json:"id" db:"id"`
	FullName string `json:"full_name" db:"full_name"`
	Email    string `json:"email" db:"email"`
	Role     string `json:"role" db:"role"`
	Age      *int   `json:"age,omitempty" db:"age"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type OptionInput struct {
	Text      string `json:"text" binding:"required"`
	IsCorrect bool   `json:"is_correct"`
}

type QuestionInput struct {
	Text    string        `json:"text" binding:"required"`
	Type    string        `json:"type"`
	Points  int           `json:"points"`
	Options []OptionInput `json:"options"`
}

type CreateRoomRequest struct {
	Title                 string          `json:"title" binding:"required"`
	Description           string          `json:"description"`
	DurationMinutes       int             `json:"duration_minutes" binding:"required"`
	ShowResultsToStudents bool            `json:"show_results_to_students"`
	PassMarkPercentage    float64         `json:"pass_mark_percentage"`
	Questions             []QuestionInput `json:"questions" binding:"required"`
}

type Room struct {
	ID                    string     `json:"id" db:"id"`
	TeacherID             string     `json:"teacher_id" db:"teacher_id"`
	Title                 string     `json:"title" db:"title"`
	Description           *string    `json:"description,omitempty" db:"description"`
	RoomCode              string     `json:"room_code" db:"room_code"`
	DurationMinutes       int        `json:"duration_minutes" db:"duration_minutes"`
	Status                string     `json:"status" db:"status"`
	ShowResultsToStudents bool       `json:"show_results_to_students" db:"show_results_to_students"`
	PassMarkPercentage    float64    `json:"pass_mark_percentage" db:"pass_mark_percentage"`
	StartsAt              *time.Time `json:"starts_at,omitempty" db:"starts_at"`
	ClosesAt              *time.Time `json:"closes_at,omitempty" db:"closes_at"`
	ClosedAt              *time.Time `json:"closed_at,omitempty" db:"closed_at"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
}

type PublicOption struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type PublicQuestion struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Type     string         `json:"type"`
	Points   int            `json:"points"`
	Position int            `json:"position"`
	Options  []PublicOption `json:"options"`
}

type AnswerInput struct {
	QuestionID       string `json:"question_id" binding:"required"`
	SelectedOptionID string `json:"selected_option_id"`
	TextAnswer       string `json:"text_answer"`
}

type SubmitRequest struct {
	Answers []AnswerInput `json:"answers"`
}

type Result struct {
	ID           string    `json:"id" db:"id"`
	RoomID       string    `json:"room_id" db:"room_id"`
	StudentID    string    `json:"student_id" db:"student_id"`
	SubmissionID string    `json:"submission_id" db:"submission_id"`
	Score        int       `json:"score" db:"score"`
	TotalPoints  int       `json:"total_points" db:"total_points"`
	Percentage   float64   `json:"percentage" db:"percentage"`
	Grade        string    `json:"grade" db:"grade"`
	Passed       bool      `json:"passed" db:"passed"`
	CalculatedAt time.Time `json:"calculated_at" db:"calculated_at"`
}
