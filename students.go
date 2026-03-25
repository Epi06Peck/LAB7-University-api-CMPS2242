package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// struct model

type Student struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Programme string `json:"programme"`
	Year      int    `json:"year"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// GET /students
// ③ READ — list all students from PostgreSQL.
//
// Query pattern:
//   - db.QueryContext returns a *sql.Rows cursor.
//   - We always defer rows.Close() immediately after checking the error so the
//     underlying connection is returned to the pool when we're done, even if
//     we return early.
//   - rows.Next() advances the cursor one row at a time.
//   - rows.Scan() maps each column into a Go variable by position.
//   - rows.Err() must be checked after the loop: the loop exits on both
//     "no more rows" and "a read error occurred". Err() tells us which.
func (app *application) listStudents(w http.ResponseWriter, r *http.Request) {

	// ③-a  The query. $1, $2 … are PostgreSQL placeholders (not ?).
	//       We pass no arguments here, but the pattern is the same when we do.
	query := `
		SELECT id, name, programme, year
		FROM students
		ORDER BY id`

	// ③-b  Build the context that governs this DB call.
	//       r.Context() is the parent — it cancels if the HTTP client disconnects.
	//       WithTimeout adds a hard 3-second deadline on top of that.
	//       The call is cancelled by whichever fires first.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second) //r.context one more restriction:
	// kill query if browser was closed.
	defer cancel()

	rows, err := app.db.QueryContext(ctx, query) //db.QueryContext -> gives databse creds -> found in the app struct
	//Pass context followed by the query.
	if err != nil {
		app.serverError(w, err) // POSSIBLe error returned from db if any rows were returned.
		return
	}
	defer rows.Close() // ③-c  Return the connection to the pool when done. We clean up since it uses memory

	// ③-d  Accumulate results into a slice.
	var students []Student

	for rows.Next() { // each object returned put it in an array -> "next" iterates for us
		var s Student
		// ③-e  Scan maps columns → Go variables in SELECT order.
		err := rows.Scan(&s.ID, &s.Name, &s.Programme, &s.Year)
		if err != nil {
			app.serverError(w, err)
			return
		}
		students = append(students, s) // after errors were checked we add to students array (append)
	}

	// ③-f  rows.Err() surfaces any error that stopped the loop early.
	if err = rows.Err(); err != nil {
		app.serverError(w, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"students": students}, nil)
	if err != nil {
		app.serverError(w, err)
	}
}

// GET /students/{id}
// ④ READ — fetch a single student by primary key.
//
// Query pattern:
//   - db.QueryRowContext returns exactly one *sql.Row (never nil).
//   - row.Scan() returns sql.ErrNoRows when there is no matching record.
//     We translate that into a 404; all other errors are 500.
func (app *application) getStudent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		app.notFound(w)
		return
	}

	// ④-a  QueryRowContext is used when we expect at most one row.
	//       $1 is bound to the id argument — the driver escapes it safely,
	//       preventing SQL injection.
	query := `
		SELECT id, name, programme, year
		FROM students
		WHERE id = $1`

	var s Student

	// ④-b  Build the context: r.Context() as parent (client disconnect),
	//       WithTimeout as the hard deadline (3 seconds).
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// ④-c  Scan is called directly on the *sql.Row, not inside a loop.
	err = app.db.QueryRowContext(ctx, query, id).Scan(
		&s.ID, &s.Name, &s.Programme, &s.Year,
	)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows): // ④-d  No match → 404.
			app.notFound(w)
		default:
			app.serverError(w, err)
		}
		return
	}

	extra := http.Header{"X-Resource-Id": []string{strconv.FormatInt(id, 10)}}
	err = app.writeJSON(w, http.StatusOK, envelope{"student": s}, extra)
	if err != nil {
		app.serverError(w, err)
	}
}

// POST /students
// ⑤ WRITE — insert a new student and return its generated ID.
//
// Query pattern:
//   - We use INSERT … RETURNING id to capture the auto-generated primary key
//     in a single round-trip (no need for a separate SELECT after the insert).
//   - QueryRowContext + Scan is used instead of ExecContext because RETURNING
//     produces a result row.
func (app *application) createStudent(w http.ResponseWriter, r *http.Request) {

	var input struct {
		Name      string `json:"name"`
		Programme string `json:"programme"`
		Year      int    `json:"year"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequest(w, err.Error())
		return
	}

	v := newValidator()
	v.Check(input.Name != "", "name", "must be provided")
	v.Check(len(input.Name) <= 100, "name", "must not exceed 100 characters")
	v.Check(input.Programme != "", "programme", "must be provided")
	v.Check(between(input.Year, 1, 4), "year", "must be between 1 and 4")

	if !v.Valid() {
		app.failedValidation(w, v.Errors)
		return
	}

	// ⑤-a  INSERT with RETURNING.
	//       Parameterised placeholders ($1, $2, $3) bind our Go values.
	//       PostgreSQL fills in id automatically (BIGSERIAL in the DDL).
	query := `
		INSERT INTO students (name, programme, year)
		VALUES ($1, $2, $3)
		RETURNING id`

	var newID int64

	// ⑤-b  Build the context: r.Context() as parent, 3-second hard deadline.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// ⑤-c  QueryRowContext executes the statement and Scan captures the
	//       returned id column. One DB round-trip does the whole job.
	err = app.db.QueryRowContext(ctx, query,
		input.Name, input.Programme, input.Year,
	).Scan(&newID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	newStudent := Student{
		ID:        newID,
		Name:      input.Name,
		Programme: input.Programme,
		Year:      input.Year,
	}

	extra := http.Header{
		"Location": []string{"/students/" + strconv.FormatInt(newID, 10)},
	}
	err = app.writeJSON(w, http.StatusCreated, envelope{"student": newStudent}, extra)
	if err != nil {
		app.serverError(w, err)
	}
}

// PUT /students/{id}
// ⑥ WRITE — replace a student record.
//
// Query pattern:
//   - We use ExecContext when we don't need data back from the statement.
//   - result.RowsAffected() tells us whether the WHERE clause matched anything,
//     so we can return 404 instead of silently doing nothing.
func (app *application) updateStudent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		app.notFound(w)
		return
	}

	var input struct {
		Name      string `json:"name"`
		Programme string `json:"programme"`
		Year      int    `json:"year"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequest(w, err.Error())
		return
	}

	v := newValidator()
	v.Check(input.Name != "", "name", "must be provided")
	v.Check(len(input.Name) <= 100, "name", "must not exceed 100 characters")
	v.Check(input.Programme != "", "programme", "must be provided")
	v.Check(between(input.Year, 1, 4), "year", "must be between 1 and 4")

	if !v.Valid() {
		app.failedValidation(w, v.Errors)
		return
	}

	// ⑥-a  UPDATE. $4 is the WHERE clause argument (the id from the URL).
	query := `
		UPDATE students
		SET name = $1, programme = $2, year = $3
		WHERE id = $4`

	// ⑥-b  Build the context: r.Context() as parent, 3-second hard deadline.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// ⑥-c  ExecContext is used for statements that don't return rows.
	//       It returns a sql.Result with metadata about the operation.
	result, err := app.db.ExecContext(ctx, query,
		input.Name, input.Programme, input.Year, id,
	)
	if err != nil {
		app.serverError(w, err)
		return
	}

	// ⑥-d  RowsAffected reports how many rows the UPDATE touched.
	//       0 means no student had that id → 404.
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		app.serverError(w, err)
		return
	}
	if rowsAffected == 0 {
		app.notFound(w)
		return
	}

	updated := Student{ID: id, Name: input.Name, Programme: input.Programme, Year: input.Year}
	err = app.writeJSON(w, http.StatusOK, envelope{"student": updated}, nil)
	if err != nil {
		app.serverError(w, err)
	}
}

// DELETE /students/{id}
// ⑦ WRITE — remove a student.
//
// Same ExecContext + RowsAffected pattern as PUT.
// Returns 204 No Content on success (no body needed).
func (app *application) deleteStudent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		app.notFound(w)
		return
	}

	query := `DELETE FROM students WHERE id = $1`

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	result, err := app.db.ExecContext(ctx, query, id)
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

	// 204 No Content — the resource is gone, there is nothing to send back.
	w.WriteHeader(http.StatusNoContent)
}
