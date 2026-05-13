package api

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shellhaki/openCBT/internal/models"
)

var (
	errRoomClosed     = errors.New("room closed")
	errNotParticipant = errors.New("student is not a participant")
)

func (s *Server) getOwnedRoom(ctx context.Context, roomID, teacherID string) (models.Room, error) {
	var room models.Room
	err := s.db.QueryRow(ctx, `
		SELECT id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
		FROM exam_rooms
		WHERE id = $1 AND teacher_id = $2
	`, roomID, teacherID).Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description, &room.RoomCode,
		&room.DurationMinutes, &room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage,
		&room.StartsAt, &room.ClosesAt, &room.ClosedAt, &room.CreatedAt)
	return room, err
}

func (s *Server) closeOwnedRoom(ctx context.Context, roomID, teacherID string) (models.Room, error) {
	var room models.Room
	err := s.db.QueryRow(ctx, `
		UPDATE exam_rooms
		SET status = 'closed', closed_at = NOW(), closes_at = COALESCE(closes_at, NOW())
		WHERE id = $1 AND teacher_id = $2 AND status <> 'closed'
		RETURNING id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
	`, roomID, teacherID).Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description, &room.RoomCode,
		&room.DurationMinutes, &room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage,
		&room.StartsAt, &room.ClosesAt, &room.ClosedAt, &room.CreatedAt)
	return room, err
}

func (s *Server) CloseExpiredRooms(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
		UPDATE exam_rooms
		SET status = 'closed', closed_at = NOW()
		WHERE status = 'active' AND closes_at IS NOT NULL AND closes_at <= NOW()
		RETURNING id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var roomID string
		if err := rows.Scan(&roomID); err != nil {
			return err
		}
		s.hub.CloseRoom(roomID)
	}

	return rows.Err()
}

func (s *Server) studentJoinedRoom(ctx context.Context, roomID, studentID string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM room_participants
			WHERE room_id = $1 AND student_id = $2 AND status IN ('joined', 'submitted', 'disconnected')
		)
	`, roomID, studentID).Scan(&exists)
	return err == nil && exists
}

func (s *Server) markDisconnected(roomID, userID, role string) {
	if role != "student" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = s.db.Exec(ctx, `
		UPDATE room_participants
		SET status = 'disconnected'
		WHERE room_id = $1 AND student_id = $2 AND status = 'joined'
	`, roomID, userID)
}

func (s *Server) publicExam(ctx context.Context, roomID string) (models.Room, []models.PublicQuestion, error) {
	var room models.Room
	err := s.db.QueryRow(ctx, `
		SELECT id, teacher_id, title, description, room_code, duration_minutes, status,
			show_results_to_students, pass_mark_percentage, starts_at, closes_at, closed_at, created_at
		FROM exam_rooms
		WHERE id = $1
	`, roomID).Scan(&room.ID, &room.TeacherID, &room.Title, &room.Description, &room.RoomCode,
		&room.DurationMinutes, &room.Status, &room.ShowResultsToStudents, &room.PassMarkPercentage,
		&room.StartsAt, &room.ClosesAt, &room.ClosedAt, &room.CreatedAt)
	if err != nil {
		return models.Room{}, nil, err
	}

	questionRows, err := s.db.Query(ctx, `
		SELECT id, question_text, question_type, points, position
		FROM questions
		WHERE room_id = $1
		ORDER BY position
	`, roomID)
	if err != nil {
		return models.Room{}, nil, err
	}
	defer questionRows.Close()

	questions := make([]models.PublicQuestion, 0)
	for questionRows.Next() {
		var question models.PublicQuestion
		if err := questionRows.Scan(&question.ID, &question.Text, &question.Type, &question.Points, &question.Position); err != nil {
			return models.Room{}, nil, err
		}
		questions = append(questions, question)
	}

	for i := range questions {
		optionRows, err := s.db.Query(ctx, `
			SELECT id, option_text, position
			FROM question_options
			WHERE question_id = $1
			ORDER BY position
		`, questions[i].ID)
		if err != nil {
			return models.Room{}, nil, err
		}

		options := make([]models.PublicOption, 0)
		for optionRows.Next() {
			var option models.PublicOption
			if err := optionRows.Scan(&option.ID, &option.Text, &option.Position); err != nil {
				optionRows.Close()
				return models.Room{}, nil, err
			}
			options = append(options, option)
		}
		optionRows.Close()
		questions[i].Options = options
	}

	return room, questions, nil
}

func (s *Server) calculateSubmission(ctx context.Context, roomID, studentID string, answers []models.AnswerInput) (models.Result, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return models.Result{}, err
	}
	defer rollback(ctx, tx)

	var roomStatus string
	var closesAt *time.Time
	var passMark float64
	err = tx.QueryRow(ctx, `
		SELECT status, closes_at, pass_mark_percentage
		FROM exam_rooms
		WHERE id = $1
	`, roomID).Scan(&roomStatus, &closesAt, &passMark)
	if err != nil {
		return models.Result{}, err
	}
	if roomStatus == "closed" || roomStatus == "cancelled" {
		return models.Result{}, errRoomClosed
	}

	var participantID string
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM room_participants
		WHERE room_id = $1 AND student_id = $2
	`, roomID, studentID).Scan(&participantID)
	if err != nil {
		return models.Result{}, errNotParticipant
	}

	submissionStatus := "submitted"
	if closesAt != nil && time.Now().After(*closesAt) {
		submissionStatus = "late"
	}

	totalQuestions, totalPoints, err := roomTotals(ctx, tx, roomID)
	if err != nil {
		return models.Result{}, err
	}

	var submissionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO submissions (room_id, student_id, status, total_questions, total_answered)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (room_id, student_id)
		DO UPDATE SET status = EXCLUDED.status, total_questions = EXCLUDED.total_questions,
			total_answered = EXCLUDED.total_answered, submitted_at = NOW()
		RETURNING id
	`, roomID, studentID, submissionStatus, totalQuestions, len(answers)).Scan(&submissionID)
	if err != nil {
		return models.Result{}, err
	}

	_, _ = tx.Exec(ctx, `DELETE FROM submission_answers WHERE submission_id = $1`, submissionID)

	score := 0
	for _, answer := range answers {
		isCorrect, points, err := scoreAnswer(ctx, tx, roomID, answer)
		if err != nil {
			return models.Result{}, err
		}
		if isCorrect {
			score += points
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO submission_answers
				(submission_id, question_id, selected_option_id, text_answer, is_correct, points_awarded)
			VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, ''), $5, $6)
		`, submissionID, answer.QuestionID, answer.SelectedOptionID, answer.TextAnswer, isCorrect, pointsIf(isCorrect, points))
		if err != nil {
			return models.Result{}, err
		}
	}

	percentage := 0.0
	if totalPoints > 0 {
		percentage = math.Round((float64(score)/float64(totalPoints))*10000) / 100
	}
	grade := gradeFromPercentage(percentage)
	passed := percentage >= passMark

	var result models.Result
	err = tx.QueryRow(ctx, `
		INSERT INTO results (room_id, student_id, submission_id, score, total_points, percentage, grade, passed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (room_id, student_id)
		DO UPDATE SET submission_id = EXCLUDED.submission_id, score = EXCLUDED.score,
			total_points = EXCLUDED.total_points, percentage = EXCLUDED.percentage,
			grade = EXCLUDED.grade, passed = EXCLUDED.passed, calculated_at = NOW()
		RETURNING id, room_id, student_id, submission_id, score, total_points, percentage,
			COALESCE(grade, '') AS grade, passed, calculated_at
	`, roomID, studentID, submissionID, score, totalPoints, percentage, grade, passed).
		Scan(&result.ID, &result.RoomID, &result.StudentID, &result.SubmissionID, &result.Score,
			&result.TotalPoints, &result.Percentage, &result.Grade, &result.Passed, &result.CalculatedAt)
	if err != nil {
		return models.Result{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE room_participants
		SET status = 'submitted', submitted_at = NOW()
		WHERE id = $1
	`, participantID)
	if err != nil {
		return models.Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Result{}, err
	}

	return result, nil
}

func roomTotals(ctx context.Context, tx pgx.Tx, roomID string) (int, int, error) {
	var totalQuestions int
	var totalPoints int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(points), 0)
		FROM questions
		WHERE room_id = $1
	`, roomID).Scan(&totalQuestions, &totalPoints)
	return totalQuestions, totalPoints, err
}

func scoreAnswer(ctx context.Context, tx pgx.Tx, roomID string, answer models.AnswerInput) (bool, int, error) {
	var points int
	var questionType string
	err := tx.QueryRow(ctx, `
		SELECT points, question_type
		FROM questions
		WHERE id = $1 AND room_id = $2
	`, answer.QuestionID, roomID).Scan(&points, &questionType)
	if err != nil {
		return false, 0, err
	}

	if questionType != "objective" || answer.SelectedOptionID == "" {
		return false, points, nil
	}

	var isCorrect bool
	err = tx.QueryRow(ctx, `
		SELECT is_correct
		FROM question_options
		WHERE id = $1 AND question_id = $2
	`, answer.SelectedOptionID, answer.QuestionID).Scan(&isCorrect)
	if err != nil {
		return false, 0, err
	}

	return isCorrect, points, nil
}

func pointsIf(ok bool, points int) int {
	if ok {
		return points
	}
	return 0
}

func gradeFromPercentage(percentage float64) string {
	switch {
	case percentage >= 70:
		return "A"
	case percentage >= 60:
		return "B"
	case percentage >= 50:
		return "C"
	case percentage >= 45:
		return "D"
	case percentage >= 40:
		return "E"
	default:
		return "F"
	}
}

func (s *Server) roomParticipants(ctx context.Context, roomID string) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.full_name, u.email, u.age, rp.status, rp.joined_at, rp.submitted_at
		FROM room_participants rp
		JOIN users u ON u.id = rp.student_id
		WHERE rp.room_id = $1
		ORDER BY rp.joined_at
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	participants := make([]map[string]any, 0)
	for rows.Next() {
		var id, fullName, email, status string
		var age *int
		var joinedAt time.Time
		var submittedAt *time.Time
		if err := rows.Scan(&id, &fullName, &email, &age, &status, &joinedAt, &submittedAt); err != nil {
			return nil, err
		}
		participants = append(participants, map[string]any{
			"id":           id,
			"full_name":    fullName,
			"email":        email,
			"age":          age,
			"status":       status,
			"joined_at":    joinedAt,
			"submitted_at": submittedAt,
		})
	}

	return participants, rows.Err()
}

func (s *Server) roomResults(ctx context.Context, roomID string) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, u.id, u.full_name, u.email, u.age, r.score, r.total_points,
			r.percentage, COALESCE(r.grade, '') AS grade, r.passed, r.calculated_at
		FROM results r
		JOIN users u ON u.id = r.student_id
		WHERE r.room_id = $1
		ORDER BY r.percentage DESC, u.full_name
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]map[string]any, 0)
	for rows.Next() {
		var resultID, studentID, fullName, email, grade string
		var age *int
		var score, totalPoints int
		var percentage float64
		var passed bool
		var calculatedAt time.Time
		if err := rows.Scan(&resultID, &studentID, &fullName, &email, &age, &score, &totalPoints, &percentage, &grade, &passed, &calculatedAt); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{
			"id":            resultID,
			"student_id":    studentID,
			"full_name":     fullName,
			"email":         email,
			"age":           age,
			"score":         score,
			"total_points":  totalPoints,
			"percentage":    percentage,
			"grade":         grade,
			"passed":        passed,
			"calculated_at": calculatedAt,
		})
	}

	return results, rows.Err()
}
