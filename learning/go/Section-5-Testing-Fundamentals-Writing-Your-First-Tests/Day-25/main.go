package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

// ============================================================
// TASK SERVICE
// ============================================================

type TaskService struct {
	mu     sync.Mutex
	tasks  []Task
	nextID int
}

func NewTaskService() *TaskService {
	return &TaskService{
		tasks:  make([]Task, 0),
		nextID: 1,
	}
}

func (s *TaskService) Create(title string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := Task{
		ID:        s.nextID,
		Title:     title,
		Completed: false,
	}

	s.tasks = append(s.tasks, task)
	s.nextID++

	return task
}

func (s *TaskService) Complete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].Completed = true
			return true
		}
	}

	return false
}

func (s *TaskService) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Task, len(s.tasks))
	copy(result, s.tasks)

	return result
}

// ============================================================
// BUSINESS LOGIC
// ============================================================

// Task title validation.
//
// Bu fonksiyon özellikle test edilebilir küçük bir
// business-logic unit'i olarak tasarlandı.
func validateTitle(title string) error {

	title = strings.TrimSpace(title)

	if title == "" {
		return fmt.Errorf("title cannot be empty")
	}

	if len(title) < 3 {
		return fmt.Errorf("title must contain at least 3 characters")
	}

	return nil
}

// ============================================================
// HTTP HANDLER
// ============================================================

type Handler struct {
	service *TaskService
}

func NewHandler(service *TaskService) *Handler {
	return &Handler{
		service: service,
	}
}

// POST /tasks
//
// Body:
//
//	{
//	    "title": "Learn Go"
//	}
func (h *Handler) CreateTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	type request struct {
		Title string `json:"title"`
	}

	var req request

	err := json.NewDecoder(r.Body).Decode(&req)

	if err != nil {
		http.Error(
			w,
			"invalid JSON",
			http.StatusBadRequest,
		)

		return
	}

	if err := validateTitle(req.Title); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	task := h.service.Create(req.Title)

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(task)
}

// GET /tasks
func (h *Handler) ListTasks(
	w http.ResponseWriter,
	r *http.Request,
) {

	tasks := h.service.List()

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	json.NewEncoder(w).Encode(tasks)
}

// ============================================================
// TASK 1
// TABLE-DRIVEN TEST
// ============================================================

func TestValidateTitle(t *testing.T) {

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid title",
			input:   "Learn Go",
			wantErr: false,
		},
		{
			name:    "another valid title",
			input:   "Build API",
			wantErr: false,
		},
		{
			name:    "empty title",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace title",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "too short",
			input:   "Go",
			wantErr: true,
		},
		{
			name:    "boundary valid title",
			input:   "Go!",
			wantErr: false,
		},
	}

	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {

			err := validateTitle(tt.input)

			gotErr := err != nil

			if gotErr != tt.wantErr {
				t.Errorf(
					"validateTitle(%q) error = %v, wantErr = %v",
					tt.input,
					err,
					tt.wantErr,
				)
			}
		})
	}
}

// ============================================================
// TASK 1 - SERVICE TESTS
// ============================================================

func TestTaskService(t *testing.T) {

	service := NewTaskService()

	task := service.Create("Learn Go")

	if task.ID != 1 {
		t.Errorf(
			"got ID %d, want 1",
			task.ID,
		)
	}

	if task.Title != "Learn Go" {
		t.Errorf(
			"got title %q, want %q",
			task.Title,
			"Learn Go",
		)
	}

	if task.Completed {
		t.Error("new task should not be completed")
	}

	ok := service.Complete(task.ID)

	if !ok {
		t.Error("expected task to be completed")
	}

	tasks := service.List()

	if len(tasks) != 1 {
		t.Fatalf(
			"got %d tasks, want 1",
			len(tasks),
		)
	}

	if !tasks[0].Completed {
		t.Error("task should be completed")
	}
}

// ============================================================
// TASK 2
// BENCHMARK
// ============================================================

func BenchmarkValidateTitle(b *testing.B) {

	title := "Build a production Go API"

	for b.Loop() {

		_ = validateTitle(title)
	}
}

// ============================================================
// TASK 3
// HTTP HANDLER TEST
// ============================================================

func TestCreateTaskHandler(t *testing.T) {

	service := NewTaskService()

	handler := NewHandler(service)

	body := strings.NewReader(
		`{"title":"Learn Testing"}`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		body,
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	recorder := httptest.NewRecorder()

	handler.CreateTask(
		recorder,
		request,
	)

	// HTTP status
	if recorder.Code != http.StatusCreated {

		t.Errorf(
			"got status %d, want %d",
			recorder.Code,
			http.StatusCreated,
		)
	}

	// JSON response
	var task Task

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&task,
	)

	if err != nil {

		t.Fatalf(
			"invalid JSON response: %v",
			err,
		)
	}

	if task.ID != 1 {
		t.Errorf(
			"got ID %d, want 1",
			task.ID,
		)
	}

	if task.Title != "Learn Testing" {

		t.Errorf(
			"got title %q, want %q",
			task.Title,
			"Learn Testing",
		)
	}

	if task.Completed {
		t.Error("new task should not be completed")
	}
}

// ============================================================
// TASK 3 - HTTP FAILURE CASE
// ============================================================

func TestCreateTaskHandlerInvalidJSON(t *testing.T) {

	service := NewTaskService()

	handler := NewHandler(service)

	body := strings.NewReader(
		`{"title":`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		body,
	)

	recorder := httptest.NewRecorder()

	handler.CreateTask(
		recorder,
		request,
	)

	if recorder.Code != http.StatusBadRequest {

		t.Errorf(
			"got status %d, want %d",
			recorder.Code,
			http.StatusBadRequest,
		)
	}
}

// ============================================================
// TASK 3 - HTTP VALIDATION FAILURE
// ============================================================

func TestCreateTaskHandlerInvalidTitle(t *testing.T) {

	service := NewTaskService()

	handler := NewHandler(service)

	body := strings.NewReader(
		`{"title":"Go"}`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		body,
	)

	recorder := httptest.NewRecorder()

	handler.CreateTask(
		recorder,
		request,
	)

	if recorder.Code != http.StatusBadRequest {

		t.Errorf(
			"got status %d, want %d",
			recorder.Code,
			http.StatusBadRequest,
		)
	}
}

// ============================================================
// TASK 3 - GET HANDLER
// ============================================================

func TestListTasksHandler(t *testing.T) {

	service := NewTaskService()

	service.Create("Learn Go")
	service.Create("Write Tests")

	handler := NewHandler(service)

	request := httptest.NewRequest(
		http.MethodGet,
		"/tasks",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ListTasks(
		recorder,
		request,
	)

	if recorder.Code != http.StatusOK {

		t.Errorf(
			"got status %d, want %d",
			recorder.Code,
			http.StatusOK,
		)
	}

	var tasks []Task

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&tasks,
	)

	if err != nil {

		t.Fatalf(
			"invalid JSON response: %v",
			err,
		)
	}

	if len(tasks) != 2 {

		t.Fatalf(
			"got %d tasks, want 2",
			len(tasks),
		)
	}
}

// ============================================================
// TASK 4
// INTENTIONAL FAILURE
// ============================================================

// Bu test normalde BAŞARISIZ olacak şekilde yazılmıştır.
//
// Amacımız failure output okumayı göstermek.
//
// Sonrasında want değerini düzeltiyoruz.
func TestIntentionalFailureExample(t *testing.T) {

	got := validateTitle("Go")

	// İlk durumda yanlış beklenti:
	//
	// wantErr := false
	//
	// Bu durumda test fail eder.
	//
	//	var wantErr = false

	// FIX:
	wantErr := true

	gotErr := got != nil

	if gotErr != wantErr {

		t.Errorf(
			"validateTitle(%q) error = %v, wantErr = %v",
			"Go",
			got,
			wantErr,
		)
	}
}

// ============================================================
// REGRESSION TEST
// ============================================================

// Daha önce validateTitle fonksiyonunda whitespace
// problemi olduğunu düşünelim:
//
// "   "
//
// boş string gibi davranmalı.
//
// Bu test artık bug'ın tekrar gelmesini engelliyor.
func TestValidateTitleWhitespaceRegression(t *testing.T) {

	err := validateTitle("   ")

	if err == nil {

		t.Error(
			"whitespace-only title should return an error",
		)
	}
}

// ============================================================
// MAIN
// ============================================================

func main() {

	service := NewTaskService()

	handler := NewHandler(service)

	http.HandleFunc(
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {

			case http.MethodGet:
				handler.ListTasks(w, r)

			case http.MethodPost:
				handler.CreateTask(w, r)

			default:
				http.Error(
					w,
					"method not allowed",
					http.StatusMethodNotAllowed,
				)
			}
		},
	)

	fmt.Println(
		"Task API running on :8080",
	)

	err := http.ListenAndServe(
		":8080",
		nil,
	)

	if err != nil {
		panic(err)
	}
}
