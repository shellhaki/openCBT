# openCBT

Self-hosted open source CBT backend built with Go, Gin, PostgreSQL, REST, and WebSockets.

## Current Backend Scope

- Students and teachers can sign up and log in.
- Teachers can create exam rooms with objective/theory questions.
- Teachers can start, close, list, and inspect their rooms.
- Students can join rooms by room code, fetch exam questions, and submit answers.
- Objective answers are graded automatically and stored in `results`.
- Students only see their results when the teacher enables `show_results_to_students`.
- Room sockets are closed when a teacher closes a room or when the duration expires.

## Setup

Create a `.env` file:

```env
POSTGRES_URI=postgres://user:password@localhost:5432/opencbt?sslmode=disable
JWT_SECRET=replace-this-secret
JWT_TTL_HOURS=24
PORT=8080
```

Run the schema:

```bash
psql "$POSTGRES_URI" -f src/schema/query.sql
```

Start the API:

```bash
go run .
```

## REST API

Auth:

- `POST /api/auth/signup`
- `POST /api/auth/login`

Teacher routes require `Authorization: Bearer <token>` from a teacher account:

- `POST /api/teacher/rooms`
- `GET /api/teacher/rooms`
- `GET /api/teacher/rooms/:room_id`
- `POST /api/teacher/rooms/:room_id/start`
- `POST /api/teacher/rooms/:room_id/close`

Student routes require `Authorization: Bearer <token>` from a student account:

- `POST /api/student/rooms/join`
- `GET /api/student/rooms/:room_id/exam`
- `POST /api/student/rooms/:room_id/submit`
- `GET /api/student/results`

WebSocket:

- `GET /ws/rooms/:room_id?token=<jwt>`

## Example Room Payload

```json
{
  "title": "Biology Mock Exam",
  "description": "First term practice",
  "duration_minutes": 45,
  "show_results_to_students": true,
  "pass_mark_percentage": 50,
  "questions": [
    {
      "text": "What is the powerhouse of the cell?",
      "type": "objective",
      "points": 1,
      "options": [
        { "text": "Nucleus", "is_correct": false },
        { "text": "Mitochondria", "is_correct": true },
        { "text": "Ribosome", "is_correct": false }
      ]
    }
  ]
}
```
