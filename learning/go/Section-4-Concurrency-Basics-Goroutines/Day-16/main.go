package main

import (
	"fmt"
	"sync"
	"time"
)

// ============================================================
// MODEL
// ============================================================

type Task struct {
	ID   int
	Name string
}

// ============================================================
// WORKER
// ============================================================

type TaskProcessor struct {
	Tasks []Task
}

// NewTaskProcessor yeni processor oluşturur.
func NewTaskProcessor(tasks []Task) *TaskProcessor {
	return &TaskProcessor{
		Tasks: tasks,
	}
}

// processTask tek bir task'ı işler.
func (tp *TaskProcessor) processTask(task Task) {
	fmt.Printf(
		"[Worker] Processing task %d: %s\n",
		task.ID,
		task.Name,
	)

	// Gerçek bir işlem yapıyormuşuz gibi
	// küçük bir gecikme simüle ediyoruz.
	time.Sleep(100 * time.Millisecond)

	fmt.Printf(
		"[Worker] Finished task %d\n",
		task.ID,
	)
}

// ============================================================
// TASK 1 + TASK 2
//
// Goroutines + WaitGroup
// ============================================================

func (tp *TaskProcessor) ProcessConcurrently() {
	fmt.Println("\n========================================")
	fmt.Println("CONCURRENT PROCESSING")
	fmt.Println("========================================")

	var wg sync.WaitGroup

	for _, task := range tp.Tasks {
		wg.Add(1)

		go func(t Task) {
			defer wg.Done()

			tp.processTask(t)
		}(task)
	}

	fmt.Println("Waiting for workers...")

	wg.Wait()

	fmt.Println("All tasks completed.")
}

// ============================================================
// TASK 3
//
// DELIBERATE DATA RACE
// ============================================================

// Bu fonksiyon özellikle GÜVENLİ DEĞİLDİR.
//
// Eğitim amacıyla data race oluşturuyoruz.
func demonstrateRace() {
	fmt.Println("\n========================================")
	fmt.Println("DATA RACE DEMO")
	fmt.Println("========================================")

	counter := 0

	var wg sync.WaitGroup

	const workers = 100

	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()

			// UNSAFE
			counter++
		}()
	}

	wg.Wait()

	fmt.Println(
		"Counter:",
		counter,
	)

	fmt.Println(
		"Expected:",
		workers,
	)
}

// ============================================================
// SAFE VERSION
//
// Data race'in nasıl düzeltileceğini de gösteriyoruz.
// ============================================================

func demonstrateSafeCounter() {
	fmt.Println("\n========================================")
	fmt.Println("SAFE SHARED STATE")
	fmt.Println("========================================")

	counter := 0

	var wg sync.WaitGroup

	var mu sync.Mutex

	const workers = 100

	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()

			// Critical section.
			mu.Lock()

			counter++

			mu.Unlock()
		}()
	}

	wg.Wait()

	fmt.Println(
		"Counter:",
		counter,
	)

	fmt.Println(
		"Expected:",
		workers,
	)
}

// ============================================================
// ANOTHER CONCURRENT WORKER EXAMPLE
// ============================================================

func worker(
	id int,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	fmt.Printf(
		"Worker %d started\n",
		id,
	)

	time.Sleep(
		time.Duration(id) * 100 * time.Millisecond,
	)

	fmt.Printf(
		"Worker %d finished\n",
		id,
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	fmt.Println("========================================")
	fmt.Println("     DAY 16 - CONCURRENCY BASICS")
	fmt.Println("========================================")

	// --------------------------------------------------------
	// 1. TASK COLLECTION
	// --------------------------------------------------------

	tasks := []Task{
		{
			ID:   1,
			Name: "Load configuration",
		},
		{
			ID:   2,
			Name: "Read input file",
		},
		{
			ID:   3,
			Name: "Process data",
		},
		{
			ID:   4,
			Name: "Generate report",
		},
		{
			ID:   5,
			Name: "Save JSON",
		},
	}

	processor := NewTaskProcessor(tasks)

	// --------------------------------------------------------
	// 2. TASK 1 + TASK 2
	//
	// Goroutines + WaitGroup
	// --------------------------------------------------------

	processor.ProcessConcurrently()

	// --------------------------------------------------------
	// 3. EXTRA WORKER DEMO
	// --------------------------------------------------------

	fmt.Println("\n========================================")
	fmt.Println("WORKER DEMO")
	fmt.Println("========================================")

	var wg sync.WaitGroup

	const workerCount = 3

	wg.Add(workerCount)

	for i := 1; i <= workerCount; i++ {
		go worker(i, &wg)
	}

	wg.Wait()

	fmt.Println("All workers finished.")

	// --------------------------------------------------------
	// 4. TASK 3
	//
	// Deliberate data race.
	// --------------------------------------------------------

	demonstrateRace()

	// --------------------------------------------------------
	// 5. SAFE VERSION
	// --------------------------------------------------------

	demonstrateSafeCounter()

	// --------------------------------------------------------
	// 6. TASK 4
	//
	// Race detector is NOT activated from inside
	// the program. Run the application with:
	//
	// go run -race .
	//
	// --------------------------------------------------------

	fmt.Println("\n========================================")
	fmt.Println("RACE DETECTOR")
	fmt.Println("========================================")

	fmt.Println(
		"Run this program with:",
	)

	fmt.Println(
		"    go run -race .",
	)

	fmt.Println(
		"to detect the deliberate data race.",
	)

	fmt.Println("\n========================================")
	fmt.Println("DAY 16 COMPLETED")
	fmt.Println("========================================")
}
