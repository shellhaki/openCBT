BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- =========================
-- USERS
-- =========================
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    full_name VARCHAR(150) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,

    role VARCHAR(20) NOT NULL CHECK (role IN ('student', 'teacher')),

    age INT CHECK (age IS NULL OR age > 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =========================
-- EXAM ROOMS
-- =========================
CREATE TABLE IF NOT EXISTS exam_rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    teacher_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    title VARCHAR(200) NOT NULL,
    description TEXT,

    room_code VARCHAR(30) UNIQUE NOT NULL,

    duration_minutes INT NOT NULL CHECK (duration_minutes > 0),

    status VARCHAR(20) NOT NULL DEFAULT 'waiting'
        CHECK (status IN ('waiting', 'active', 'closed', 'cancelled')),

    show_results_to_students BOOLEAN NOT NULL DEFAULT FALSE,

    pass_mark_percentage NUMERIC(5,2) NOT NULL DEFAULT 50.00,

    starts_at TIMESTAMPTZ,
    closes_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =========================
-- QUESTIONS
-- =========================
CREATE TABLE IF NOT EXISTS questions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    room_id UUID NOT NULL REFERENCES exam_rooms(id) ON DELETE CASCADE,

    question_text TEXT NOT NULL,

    question_type VARCHAR(20) NOT NULL DEFAULT 'objective'
        CHECK (question_type IN ('objective', 'theory')),

    points INT NOT NULL DEFAULT 1 CHECK (points > 0),

    position INT NOT NULL CHECK (position > 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(room_id, position)
);

-- =========================
-- QUESTION OPTIONS
-- =========================
CREATE TABLE IF NOT EXISTS question_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,

    option_text TEXT NOT NULL,

    is_correct BOOLEAN NOT NULL DEFAULT FALSE,

    position INT NOT NULL CHECK (position > 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(question_id, position)
);

-- =========================
-- ROOM PARTICIPANTS
-- Tracks students that joined an exam room
-- =========================
CREATE TABLE IF NOT EXISTS room_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    room_id UUID NOT NULL REFERENCES exam_rooms(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    status VARCHAR(20) NOT NULL DEFAULT 'joined'
        CHECK (status IN ('joined', 'submitted', 'disconnected', 'kicked')),

    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    submitted_at TIMESTAMPTZ,

    UNIQUE(room_id, student_id)
);

-- =========================
-- SUBMISSIONS
-- One final submission per student per room
-- =========================
CREATE TABLE IF NOT EXISTS submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    room_id UUID NOT NULL REFERENCES exam_rooms(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    status VARCHAR(20) NOT NULL DEFAULT 'submitted'
        CHECK (status IN ('submitted', 'late', 'auto_submitted')),

    total_questions INT NOT NULL DEFAULT 0,
    total_answered INT NOT NULL DEFAULT 0,

    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(room_id, student_id)
);

-- =========================
-- SUBMISSION ANSWERS
-- Stores each answer from a student's submission
-- =========================
CREATE TABLE IF NOT EXISTS submission_answers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    submission_id UUID NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,

    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,

    selected_option_id UUID REFERENCES question_options(id) ON DELETE SET NULL,

    text_answer TEXT,

    is_correct BOOLEAN NOT NULL DEFAULT FALSE,

    points_awarded INT NOT NULL DEFAULT 0 CHECK (points_awarded >= 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(submission_id, question_id)
);

-- =========================
-- RESULTS
-- Stores calculated exam results
-- =========================
CREATE TABLE IF NOT EXISTS results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    room_id UUID NOT NULL REFERENCES exam_rooms(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    submission_id UUID NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,

    score INT NOT NULL DEFAULT 0 CHECK (score >= 0),
    total_points INT NOT NULL DEFAULT 0 CHECK (total_points >= 0),

    percentage NUMERIC(5,2) NOT NULL DEFAULT 0.00,

    grade VARCHAR(10),

    passed BOOLEAN NOT NULL DEFAULT FALSE,

    calculated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(room_id, student_id)
);

-- =========================
-- INDEXES
-- =========================
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE INDEX IF NOT EXISTS idx_exam_rooms_teacher_id ON exam_rooms(teacher_id);
CREATE INDEX IF NOT EXISTS idx_exam_rooms_room_code ON exam_rooms(room_code);
CREATE INDEX IF NOT EXISTS idx_exam_rooms_status ON exam_rooms(status);

CREATE INDEX IF NOT EXISTS idx_questions_room_id ON questions(room_id);

CREATE INDEX IF NOT EXISTS idx_question_options_question_id ON question_options(question_id);

CREATE INDEX IF NOT EXISTS idx_room_participants_room_id ON room_participants(room_id);
CREATE INDEX IF NOT EXISTS idx_room_participants_student_id ON room_participants(student_id);

CREATE INDEX IF NOT EXISTS idx_submissions_room_id ON submissions(room_id);
CREATE INDEX IF NOT EXISTS idx_submissions_student_id ON submissions(student_id);

CREATE INDEX IF NOT EXISTS idx_submission_answers_submission_id ON submission_answers(submission_id);
CREATE INDEX IF NOT EXISTS idx_submission_answers_question_id ON submission_answers(question_id);

CREATE INDEX IF NOT EXISTS idx_results_room_id ON results(room_id);
CREATE INDEX IF NOT EXISTS idx_results_student_id ON results(student_id);

-- =========================
-- UPDATED_AT TRIGGER
-- =========================
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_users_updated_at ON users;
CREATE TRIGGER set_users_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS set_exam_rooms_updated_at ON exam_rooms;
CREATE TRIGGER set_exam_rooms_updated_at
BEFORE UPDATE ON exam_rooms
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

COMMIT;
