package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

 	"github.com/lib/pq"
)

// struct model
type Course struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	Credits     int      `json:"credits"`
	Enrolled    int      `json:"enrolled"`
	Instructors []string `json:"instructors"`
}

// GET /courses
func (app *application) listCourses(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT code, title, credits, enrolled, instructors
		FROM courses
		ORDER BY code`

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	rows, err := app.db.QueryContext(ctx, query)
	if err != nil {
		app.serverError(w, err)
		return
	}
	defer rows.Close()

	var courses []Course

	for rows.Next() {
		var c Course
		err := rows.Scan(&c.Code, &c.Title, &c.Credits, &c.Enrolled, pq.Array(&c.Instructors),)
		if err != nil {
			app.serverError(w, err)
			return
		}
		courses = append(courses, c)
	}

	if err = rows.Err(); err != nil {
		app.serverError(w, err)
		return
	}

	app.writeJSON(w, http.StatusOK, envelope{"courses": courses}, nil)
}

// GET /courses/{code}
func (app *application) getCourse(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		app.notFound(w)
		return
	}

	query := `
		SELECT code, title, credits, enrolled, instructors
		FROM courses
		WHERE code = $1`

	var c Course

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	err := app.db.QueryRowContext(ctx, query, code).
		Scan(&c.Code, &c.Title, &c.Credits, &c.Enrolled, pq.Array(&c.Instructors),)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			app.notFound(w)
		default:
			app.serverError(w, err)
		}
		return
	}

	app.writeJSON(w, http.StatusOK, envelope{"course": c}, nil)
}

// POST /courses
func (app *application) createCourse(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code        string   `json:"code"`
		Title       string   `json:"title"`
		Credits     int      `json:"credits"`
		Enrolled    int      `json:"enrolled"`
		Instructors []string `json:"instructors"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequest(w, err.Error())
		return
	}

	v := newValidator()
	v.Check(input.Code != "", "code", "must be provided")
	v.Check(input.Title != "", "title", "must be provided")
	v.Check(input.Credits > 0, "credits", "must be greater than 0")

	if !v.Valid() {
		app.failedValidation(w, v.Errors)
		return
	}

	query := `
		INSERT INTO courses (code, title, credits, enrolled, instructors)
		VALUES ($1, $2, $3, $4, $5)`

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	_, err = app.db.ExecContext(ctx, query,
	input.Code,
	input.Title,
	input.Credits,
	input.Enrolled,
	pq.Array(input.Instructors), 
)
	if err != nil {
		app.serverError(w, err)
		return
	}

	course := Course{
		Code:        input.Code,
		Title:       input.Title,
		Credits:     input.Credits,
		Enrolled:    input.Enrolled,
		Instructors: input.Instructors,
	}

	app.writeJSON(w, http.StatusCreated, envelope{"course": course}, nil)
}

// PUT /courses/{code}
func (app *application) updateCourse(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		app.notFound(w)
		return
	}

	var input struct {
		Title       string   `json:"title"`
		Credits     int      `json:"credits"`
		Enrolled    int      `json:"enrolled"`
		Instructors []string `json:"instructors"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequest(w, err.Error())
		return
	}

	query := `
		UPDATE courses
		SET title = $1, credits = $2, enrolled = $3, instructors = $4
		WHERE code = $5`

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	result, err := app.db.ExecContext(ctx,
		query,
		input.Title,
		input.Credits,
		input.Enrolled,
		pq.Array(input.Instructors),
		code,
	)
	if err != nil {
		app.serverError(w, err)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		app.serverError(w, err)
		return
	}
	if rowsAffected == 0 {
		app.notFound(w)
		return
	}

	course := Course{
		Code:        code,
		Title:       input.Title,
		Credits:     input.Credits,
		Enrolled:    input.Enrolled,
		Instructors: input.Instructors,
	}

	app.writeJSON(w, http.StatusOK, envelope{"course": course}, nil)
}

// DELETE /courses/{code}
func (app *application) deleteCourse(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		app.notFound(w)
		return
	}

	query := `DELETE FROM courses WHERE code = $1`

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	result, err := app.db.ExecContext(ctx, query, code)
	if err != nil {
		app.serverError(w, err)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		app.serverError(w, err)
		return
	}
	if rowsAffected == 0 {
		app.notFound(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}