package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

// ============================================================
// ERRORS
// ============================================================

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrInvalidTask  = errors.New("task title is required")
)

// ============================================================
// STORE INTERFACE
// ============================================================
//
// Task 1:
// Create a Store Interface
//
// Business logic storage implementasyonunun detaylarını
// bilmez.
//
// InMemoryStore, PostgreSQLStore veya başka bir implementasyon
// bu interface'i implement edebilir.
// ============================================================

type Store interface {

	// Create yeni task oluşturur.
	Create(task Task) (Task, error)

	// Get ID üzerinden tek task döndürür.
	Get(id int) (Task, error)

	// List bütün task'ların snapshot'ını döndürür.
	List() []Task

	// Update mevcut task'ı günceller.
	Update(id int, task Task) (Task, error)

	// Delete task'ı siler.
	Delete(id int) error
}

// ============================================================
// IN-MEMORY STORE
// ============================================================
//
// Task 2:
// Implement Mutex Protection
//
// map concurrent access için güvenli değildir.
//
// Bu nedenle map'i RWMutex ile koruyoruz.
//
// RLock:
// Birden fazla reader aynı anda okuyabilir.
//
// Lock:
// Writer geldiğinde exclusive lock alınır.
// ============================================================

type InMemoryStore struct {
	mu sync.RWMutex

	tasks map[int]Task

	// Task 3:
	// Generate IDs
	//
	// Atomic counter kullanarak unique ID üretiyoruz.
	nextID int64
}

// ============================================================
// CONSTRUCTOR
// ============================================================

func NewInMemoryStore() *InMemoryStore {

	return &InMemoryStore{
		tasks: make(map[int]Task),
	}
}

// ============================================================
// CREATE
// ============================================================

func (s *InMemoryStore) Create(task Task) (Task, error) {

	// --------------------------------------------------------
	// Validation
	// --------------------------------------------------------

	if strings.TrimSpace(task.Title) == "" {
		return Task{}, ErrInvalidTask
	}

	// --------------------------------------------------------
	// Generate unique ID
	//
	// Atomic.AddInt64 concurrent goroutine'lerde bile
	// güvenli şekilde counter artırır.
	// --------------------------------------------------------

	id := atomic.AddInt64(&s.nextID, 1)

	task.ID = int(id)

	task.Title = strings.TrimSpace(task.Title)

	// --------------------------------------------------------
	// Write operation
	//
	// Map'e yazacağımız için Lock kullanıyoruz.
	// --------------------------------------------------------

	s.mu.Lock()

	s.tasks[task.ID] = task

	s.mu.Unlock()

	return cloneTask(task), nil
}

// ============================================================
// GET
// ============================================================

func (s *InMemoryStore) Get(id int) (Task, error) {

	// --------------------------------------------------------
	// Read operation
	//
	// Map'ten sadece okuyoruz.
	// Bu nedenle RLock kullanıyoruz.
	// --------------------------------------------------------

	s.mu.RLock()

	task, ok := s.tasks[id]

	s.mu.RUnlock()

	if !ok {
		return Task{}, ErrTaskNotFound
	}

	// --------------------------------------------------------
	// Task 4:
	// Return Copies
	//
	// Internal map'teki object'i doğrudan dışarı vermiyoruz.
	// --------------------------------------------------------

	return cloneTask(task), nil
}

// ============================================================
// LIST
// ============================================================

func (s *InMemoryStore) List() []Task {

	s.mu.RLock()

	// --------------------------------------------------------
	// Internal map'teki değerleri doğrudan caller'a vermiyoruz.
	//
	// Yeni bir slice oluşturuyoruz.
	// --------------------------------------------------------

	tasks := make([]Task, 0, len(s.tasks))

	for _, task := range s.tasks {

		tasks = append(
			tasks,
			cloneTask(task),
		)
	}

	s.mu.RUnlock()

	return tasks
}

// ============================================================
// UPDATE
// ============================================================

func (s *InMemoryStore) Update(
	id int,
	task Task,
) (Task, error) {

	if strings.TrimSpace(task.Title) == "" {
		return Task{}, ErrInvalidTask
	}

	// --------------------------------------------------------
	// Update map olduğu için exclusive Lock.
	// --------------------------------------------------------

	s.mu.Lock()

	_, exists := s.tasks[id]

	if !exists {
		s.mu.Unlock()

		return Task{}, ErrTaskNotFound
	}

	// ID'nin caller tarafından değiştirilmesine izin vermiyoruz.
	task.ID = id

	task.Title = strings.TrimSpace(task.Title)

	s.tasks[id] = task

	s.mu.Unlock()

	return cloneTask(task), nil
}

// ============================================================
// DELETE
// ============================================================

func (s *InMemoryStore) Delete(id int) error {

	s.mu.Lock()

	_, exists := s.tasks[id]

	if !exists {
		s.mu.Unlock()

		return ErrTaskNotFound
	}

	delete(s.tasks, id)

	s.mu.Unlock()

	return nil
}

// ============================================================
// CLONE / COPY
// ============================================================
//
// Task 4:
// Return Copies
//
// Bugünkü Task struct'ı sadece value alanları içerdiği için
// basit bir value copy yeterli.
//
// Eğer ileride Task içerisinde:
//   []string
//   map[string]string
//   *SomeStruct
//
// gibi reference-type alanlar olursa onların da deep copy'sini
// yapmak gerekir.
// ============================================================

func cloneTask(task Task) Task {

	return Task{
		ID:          task.ID,
		Title:       task.Title,
		Description: task.Description,
		Completed:   task.Completed,
	}
}

// ============================================================
// SERVICE
// ============================================================
//
// Service artık map'i bilmiyor.
//
// Sadece Store interface'ini biliyor.
//
// Bu dependency inversion açısından önemlidir.
// ============================================================

type TaskService struct {
	store Store
}

// ============================================================
// CONSTRUCTOR
// ============================================================

func NewTaskService(store Store) *TaskService {

	return &TaskService{
		store: store,
	}
}

// ============================================================
// CREATE TASK
// ============================================================

func (s *TaskService) CreateTask(
	title string,
	description string,
) (Task, error) {

	task := Task{
		Title:       title,
		Description: description,
		Completed:   false,
	}

	return s.store.Create(task)
}

// ============================================================
// GET TASK
// ============================================================

func (s *TaskService) GetTask(id int) (Task, error) {

	return s.store.Get(id)
}

// ============================================================
// LIST TASKS
// ============================================================

func (s *TaskService) ListTasks() []Task {

	return s.store.List()
}

// ============================================================
// COMPLETE TASK
// ============================================================

func (s *TaskService) CompleteTask(id int) (Task, error) {

	task, err := s.store.Get(id)

	if err != nil {
		return Task{}, err
	}

	task.Completed = true

	return s.store.Update(id, task)
}

// ============================================================
// DELETE TASK
// ============================================================

func (s *TaskService) DeleteTask(id int) error {

	return s.store.Delete(id)
}

// ============================================================
// HTTP HANDLER
// ============================================================
//
// Bu bölüm Day 33 store'unun HTTP arkasında nasıl
// kullanılabileceğini gösteriyor.
// ============================================================

type TaskHandler struct {
	service *TaskService
}

// ============================================================
// CREATE
// ============================================================
//
// POST /tasks
//
// Örnek:
// /tasks?title=Learn%20Go&description=Repository
//
// ============================================================

func (h *TaskHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	title := r.URL.Query().Get("title")

	description := r.URL.Query().Get("description")

	task, err := h.service.CreateTask(
		title,
		description,
	)

	if err != nil {

		if errors.Is(err, ErrInvalidTask) {

			http.Error(
				w,
				err.Error(),
				http.StatusBadRequest,
			)

			return
		}

		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain",
	)

	w.WriteHeader(http.StatusCreated)

	fmt.Fprintf(
		w,
		"created task: %+v\n",
		task,
	)
}

// ============================================================
// LIST
// ============================================================
//
// GET /tasks
//
// ============================================================

func (h *TaskHandler) List(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	tasks := h.service.ListTasks()

	w.Header().Set(
		"Content-Type",
		"text/plain",
	)

	for _, task := range tasks {

		fmt.Fprintf(
			w,
			"id=%d title=%q description=%q completed=%t\n",
			task.ID,
			task.Title,
			task.Description,
			task.Completed,
		)
	}
}

// ============================================================
// GET /tasks/{id}
// ============================================================

func (h *TaskHandler) Get(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/tasks/",
	)

	id, err := strconv.Atoi(idText)

	if err != nil {

		http.Error(
			w,
			"invalid task id",
			http.StatusBadRequest,
		)

		return
	}

	task, err := h.service.GetTask(id)

	if err != nil {

		if errors.Is(err, ErrTaskNotFound) {

			http.Error(
				w,
				err.Error(),
				http.StatusNotFound,
			)

			return
		}

		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain",
	)

	fmt.Fprintf(
		w,
		"id=%d title=%q description=%q completed=%t\n",
		task.ID,
		task.Title,
		task.Description,
		task.Completed,
	)
}

// ============================================================
// COMPLETE
// ============================================================
//
// POST /tasks/{id}/complete
//
// ============================================================

func (h *TaskHandler) Complete(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/tasks/",
	)

	idText = strings.TrimSuffix(
		idText,
		"/complete",
	)

	id, err := strconv.Atoi(idText)

	if err != nil {

		http.Error(
			w,
			"invalid task id",
			http.StatusBadRequest,
		)

		return
	}

	task, err := h.service.CompleteTask(id)

	if err != nil {

		if errors.Is(err, ErrTaskNotFound) {

			http.Error(
				w,
				err.Error(),
				http.StatusNotFound,
			)

			return
		}

		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain",
	)

	fmt.Fprintf(
		w,
		"completed task: %+v\n",
		task,
	)
}

// ============================================================
// DELETE
// ============================================================
//
// DELETE /tasks/{id}
// ============================================================

func (h *TaskHandler) Delete(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodDelete {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/tasks/",
	)

	id, err := strconv.Atoi(idText)

	if err != nil {

		http.Error(
			w,
			"invalid task id",
			http.StatusBadRequest,
		)

		return
	}

	err = h.service.DeleteTask(id)

	if err != nil {

		if errors.Is(err, ErrTaskNotFound) {

			http.Error(
				w,
				err.Error(),
				http.StatusNotFound,
			)

			return
		}

		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// CONCURRENT SAFETY DEMO
// ============================================================
//
// Bu fonksiyon Store'un birden fazla goroutine tarafından
// aynı anda kullanılabildiğini göstermek için oluşturuldu.
//
// RWMutex sayesinde map üzerinde data race oluşmaz.
// ============================================================

func runConcurrentDemo(store Store) {

	var wg sync.WaitGroup

	// --------------------------------------------------------
	// 10 concurrent writer
	// --------------------------------------------------------

	for i := 0; i < 10; i++ {

		wg.Add(1)

		go func(workerID int) {

			defer wg.Done()

			for j := 0; j < 10; j++ {

				_, err := store.Create(Task{
					Title: fmt.Sprintf(
						"worker-%d-task-%d",
						workerID,
						j,
					),
					Description: "concurrent create",
				})

				if err != nil {
					fmt.Println(
						"create error:",
						err,
					)
				}
			}

		}(i)
	}

	// --------------------------------------------------------
	// 10 concurrent readers
	// --------------------------------------------------------

	for i := 0; i < 10; i++ {

		wg.Add(1)

		go func() {

			defer wg.Done()

			for j := 0; j < 10; j++ {

				_ = store.List()
			}

		}()
	}

	wg.Wait()

	fmt.Println(
		"concurrent store test completed",
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	// ========================================================
	// STORE
	// ========================================================

	store := NewInMemoryStore()

	// ========================================================
	// SERVICE
	// ========================================================

	service := NewTaskService(store)

	// ========================================================
	// CREATE INITIAL TASKS
	// ========================================================

	task1, err := service.CreateTask(
		"Learn Go",
		"Study repository pattern",
	)

	if err != nil {
		logFatal(err)
	}

	task2, err := service.CreateTask(
		"Build REST API",
		"Connect HTTP handlers to store",
	)

	if err != nil {
		logFatal(err)
	}

	fmt.Println("created:")
	fmt.Println(task1)
	fmt.Println(task2)

	// ========================================================
	// READ
	// ========================================================

	task, err := service.GetTask(task1.ID)

	if err != nil {
		logFatal(err)
	}

	fmt.Println()
	fmt.Println("read:")
	fmt.Println(task)

	// ========================================================
	// UPDATE
	// ========================================================

	updated, err := service.CompleteTask(task1.ID)

	if err != nil {
		logFatal(err)
	}

	fmt.Println()
	fmt.Println("updated:")
	fmt.Println(updated)

	// ========================================================
	// LIST
	// ========================================================

	fmt.Println()
	fmt.Println("all tasks:")

	for _, task := range service.ListTasks() {
		fmt.Println(task)
	}

	// ========================================================
	// COPY / LEAKAGE DEMO
	// ========================================================
	//
	// Get() internal map'teki Task'ın kopyasını döndürür.
	//
	// Burada task'ı değiştirsek bile store'daki gerçek değer
	// değişmez.
	// ========================================================

	externalTask, err := service.GetTask(task2.ID)

	if err != nil {
		logFatal(err)
	}

	externalTask.Title = "I changed the external copy"

	storedTask, err := service.GetTask(task2.ID)

	if err != nil {
		logFatal(err)
	}

	fmt.Println()
	fmt.Println("external copy:")
	fmt.Println(externalTask)

	fmt.Println()
	fmt.Println("actual store value:")
	fmt.Println(storedTask)

	// ========================================================
	// CONCURRENT ACCESS
	// ========================================================

	fmt.Println()
	fmt.Println("running concurrent store demo...")

	runConcurrentDemo(store)

	fmt.Println()
	fmt.Println("total tasks after concurrent demo:")
	fmt.Println(len(service.ListTasks()))

	// ========================================================
	// HTTP SERVER
	// ========================================================

	handler := &TaskHandler{
		service: service,
	}

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {

			case http.MethodPost:
				handler.Create(w, r)

			case http.MethodGet:
				handler.List(w, r)

			default:
				http.Error(
					w,
					"method not allowed",
					http.StatusMethodNotAllowed,
				)
			}
		},
	)

	mux.HandleFunc(
		"/tasks/",
		func(w http.ResponseWriter, r *http.Request) {

			if strings.HasSuffix(
				r.URL.Path,
				"/complete",
			) {

				handler.Complete(w, r)

				return
			}

			switch r.Method {

			case http.MethodGet:
				handler.Get(w, r)

			case http.MethodDelete:
				handler.Delete(w, r)

			default:
				http.Error(
					w,
					"method not allowed",
					http.StatusMethodNotAllowed,
				)
			}
		},
	)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println()
	fmt.Println("HTTP server running on http://localhost:8080")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println(
		`POST /tasks?title=Learn%20Go&description=Concurrency`,
	)
	fmt.Println(`GET /tasks`)
	fmt.Println(`GET /tasks/1`)
	fmt.Println(`POST /tasks/1/complete`)
	fmt.Println(`DELETE /tasks/1`)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ============================================================
// SIMPLE FATAL HELPER
// ============================================================

func logFatal(err error) {

	panic(err)
}
