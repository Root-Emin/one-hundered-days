package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID   int
	Name string
}

type Result struct {
	TaskID int
	Worker int
	OK     bool
}

// ============================================================
// EXPENSIVE RESOURCE
// sync.Once
// ============================================================

type APIClient struct {
	Name string
}

var (
	client     *APIClient
	clientOnce sync.Once
)

// GetAPIClient guarantees that the expensive client
// is initialized exactly once.
func GetAPIClient() *APIClient {

	clientOnce.Do(func() {

		fmt.Println("Initializing expensive API client...")

		// Pretend this is expensive.
		time.Sleep(500 * time.Millisecond)

		client = &APIClient{
			Name: "Task Processing API Client",
		}

		fmt.Println("API client initialized.")
	})

	return client
}

// ============================================================
// SHARED STATS
// sync.Mutex
// ============================================================

type Stats struct {
	mu sync.Mutex

	Success int
	Failed  int
}

func (s *Stats) IncrementSuccess() {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Success++
}

func (s *Stats) IncrementFailed() {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Failed++
}

func (s *Stats) Snapshot() (int, int) {

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Success, s.Failed
}

// ============================================================
// TASK CACHE
// sync.RWMutex
// ============================================================

type TaskCache struct {
	mu    sync.RWMutex
	tasks map[int]Task
}

func NewTaskCache() *TaskCache {

	return &TaskCache{
		tasks: make(map[int]Task),
	}
}

// Write operation.
//
// Only one goroutine can write to the map.
func (c *TaskCache) Set(task Task) {

	c.mu.Lock()
	defer c.mu.Unlock()

	c.tasks[task.ID] = task
}

// Read operation.
//
// Multiple goroutines can read simultaneously.
func (c *TaskCache) Get(id int) (Task, bool) {

	c.mu.RLock()
	defer c.mu.RUnlock()

	task, exists := c.tasks[id]

	return task, exists
}

func (c *TaskCache) Size() int {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.tasks)
}

// ============================================================
// ATOMIC COUNTER
// sync/atomic
// ============================================================

// This counter contains only one simple piece of state.
//
// A Mutex would work, but atomic is lighter and clearer here.
var processedTasks atomic.Int64

// ============================================================
// WORKER
// ============================================================

func worker(
	id int,
	jobs <-chan Task,
	results chan<- Result,
	stats *Stats,
	cache *TaskCache,
) {

	// Every worker needs the API client.
	//
	// Even though every worker calls GetAPIClient(),
	// sync.Once guarantees that initialization happens once.
	client := GetAPIClient()

	fmt.Printf(
		"Worker %d started with %s\n",
		id,
		client.Name,
	)

	for task := range jobs {

		fmt.Printf(
			"Worker %d processing Task %d: %s\n",
			id,
			task.ID,
			task.Name,
		)

		// ----------------------------------------------------
		// Cache read
		// ----------------------------------------------------

		_, exists := cache.Get(task.ID)

		if !exists {

			// ------------------------------------------------
			// Cache write
			// ------------------------------------------------

			cache.Set(task)
		}

		// Simulate task processing.
		time.Sleep(100 * time.Millisecond)

		// ----------------------------------------------------
		// Atomic counter
		// ----------------------------------------------------

		processedTasks.Add(1)

		// ----------------------------------------------------
		// Simulate success/failure
		// ----------------------------------------------------

		success := task.ID%4 != 0

		if success {

			stats.IncrementSuccess()

		} else {

			stats.IncrementFailed()
		}

		// ----------------------------------------------------
		// Send result through channel
		// ----------------------------------------------------

		results <- Result{
			TaskID: task.ID,
			Worker: id,
			OK:     success,
		}
	}

	fmt.Printf("Worker %d stopped.\n", id)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	fmt.Println("========================================")
	fmt.Println("     TASK PROCESSING SERVICE")
	fmt.Println("========================================")

	// --------------------------------------------------------
	// Shared state
	// --------------------------------------------------------

	stats := &Stats{}

	cache := NewTaskCache()

	// --------------------------------------------------------
	// Channels
	// --------------------------------------------------------

	jobs := make(chan Task)

	results := make(chan Result)

	// --------------------------------------------------------
	// WaitGroups
	// --------------------------------------------------------

	var workersWG sync.WaitGroup

	// --------------------------------------------------------
	// Start workers
	// --------------------------------------------------------

	workerCount := 4

	for i := 1; i <= workerCount; i++ {

		workersWG.Add(1)

		go func(workerID int) {

			defer workersWG.Done()

			worker(
				workerID,
				jobs,
				results,
				stats,
				cache,
			)

		}(i)
	}

	// --------------------------------------------------------
	// Send jobs
	// --------------------------------------------------------

	go func() {

		for i := 1; i <= 20; i++ {

			task := Task{
				ID:   i,
				Name: fmt.Sprintf("Task-%d", i),
			}

			jobs <- task
		}

		close(jobs)

	}()

	// --------------------------------------------------------
	// Close results after all workers finish
	// --------------------------------------------------------

	go func() {

		workersWG.Wait()

		close(results)

	}()

	// --------------------------------------------------------
	// Read results
	// --------------------------------------------------------

	for result := range results {

		status := "SUCCESS"

		if !result.OK {
			status = "FAILED"
		}

		fmt.Printf(
			"Result -> Task %d | Worker %d | %s\n",
			result.TaskID,
			result.Worker,
			status,
		)
	}

	// --------------------------------------------------------
	// Final statistics
	// --------------------------------------------------------

	success, failed := stats.Snapshot()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("             FINAL REPORT")
	fmt.Println("========================================")

	fmt.Printf(
		"Processed tasks : %d\n",
		processedTasks.Load(),
	)

	fmt.Printf(
		"Successful      : %d\n",
		success,
	)

	fmt.Printf(
		"Failed          : %d\n",
		failed,
	)

	fmt.Printf(
		"Cached tasks    : %d\n",
		cache.Size(),
	)

	fmt.Println("========================================")
}
