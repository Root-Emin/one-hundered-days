package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID       int
	Name     string
	Priority string
}

type Result struct {
	TaskID   int
	TaskName string
	WorkerID int
	Status   string
}

// ============================================================
// TASK 1
// UNBUFFERED CHANNEL
// ============================================================

func demoUnbufferedChannel() {

	fmt.Println("====================================")
	fmt.Println("1. UNBUFFERED CHANNEL")
	fmt.Println("====================================")

	messageChannel := make(chan string)

	go func() {
		fmt.Println("Sender: mesaj hazırlanıyor...")

		messageChannel <- "Task sistemi hazır!"

		fmt.Println("Sender: mesaj gönderildi.")
	}()

	message := <-messageChannel

	fmt.Println("Receiver:", message)

	fmt.Println()
}

// ============================================================
// TASK 2
// BUFFERED CHANNEL
// ============================================================

func demoBufferedChannel() {

	fmt.Println("====================================")
	fmt.Println("2. BUFFERED CHANNEL")
	fmt.Println("====================================")

	// Capacity = 3
	taskChannel := make(chan string, 3)

	taskChannel <- "Prepare report"
	taskChannel <- "Send invoice"
	taskChannel <- "Backup database"

	fmt.Println("Channel capacity:", cap(taskChannel))
	fmt.Println("Channel length:", len(taskChannel))

	fmt.Println("Received:", <-taskChannel)
	fmt.Println("Received:", <-taskChannel)
	fmt.Println("Received:", <-taskChannel)

	fmt.Println()
}

// ============================================================
// TASK 3
// CLOSE + RANGE
// ============================================================

func demoCloseAndRange() {

	fmt.Println("====================================")
	fmt.Println("3. CLOSE + RANGE")
	fmt.Println("====================================")

	tasks := make(chan string, 3)

	tasks <- "Generate analytics"
	tasks <- "Sync students"
	tasks <- "Send notification"

	// Producer artık yeni task göndermeyecek.
	close(tasks)

	// Channel içindeki değerleri tüket.
	// Channel kapalı ve içerisi boş olduğunda
	// range otomatik olarak sona erer.
	for task := range tasks {
		fmt.Println("Processing:", task)
	}

	fmt.Println("All tasks consumed.")

	fmt.Println()
}

// ============================================================
// BUSINESS LOGIC
// ============================================================

func processTask(task Task, workerID int) Result {

	// Gerçek bir sistemde burada:
	// database işlemi,
	// API çağrısı,
	// dosya işlemi,
	// rapor üretimi vb. olabilir.

	time.Sleep(300 * time.Millisecond)

	status := "completed"

	return Result{
		TaskID:   task.ID,
		TaskName: task.Name,
		WorkerID: workerID,
		Status:   status,
	}
}

// ============================================================
// TASK 4
// WORKER
// ============================================================

func worker(
	id int,
	jobs <-chan Task,
	results chan<- Result,
	wg *sync.WaitGroup,
) {

	defer wg.Done()

	fmt.Printf("Worker %d started\n", id)

	// jobs channel kapanana kadar
	// yeni taskları almaya devam eder.
	for task := range jobs {

		fmt.Printf(
			"Worker %d processing Task %d: %s\n",
			id,
			task.ID,
			task.Name,
		)

		result := processTask(task, id)

		// Result sadece gönderiliyor.
		results <- result
	}

	fmt.Printf("Worker %d stopped\n", id)
}

// ============================================================
// WORKER POOL
// ============================================================

func runWorkerPool() {

	fmt.Println("====================================")
	fmt.Println("4. WORKER POOL")
	fmt.Println("====================================")

	tasks := []Task{
		{
			ID:       1,
			Name:     "Prepare monthly report",
			Priority: "high",
		},
		{
			ID:       2,
			Name:     "Send customer invoices",
			Priority: "high",
		},
		{
			ID:       3,
			Name:     "Backup database",
			Priority: "critical",
		},
		{
			ID:       4,
			Name:     "Generate analytics",
			Priority: "medium",
		},
		{
			ID:       5,
			Name:     "Sync student records",
			Priority: "medium",
		},
		{
			ID:       6,
			Name:     "Send notifications",
			Priority: "low",
		},
	}

	// --------------------------------------------------------
	// CHANNELS
	// --------------------------------------------------------

	// Producer ile workerlar arasında
	// küçük bir buffer oluşturuyoruz.
	jobs := make(chan Task, 3)

	// Workerlar sonuçları buraya gönderecek.
	results := make(chan Result, len(tasks))

	// --------------------------------------------------------
	// WORKERLARI BAŞLAT
	// --------------------------------------------------------

	workerCount := 3

	var wg sync.WaitGroup

	wg.Add(workerCount)

	for i := 1; i <= workerCount; i++ {

		go worker(
			i,
			jobs,
			results,
			&wg,
		)
	}

	// --------------------------------------------------------
	// PRODUCER
	// --------------------------------------------------------

	// Taskları jobs channel'ına gönderiyoruz.
	for _, task := range tasks {

		fmt.Printf(
			"Queueing Task %d: %s [%s]\n",
			task.ID,
			task.Name,
			task.Priority,
		)

		jobs <- task
	}

	// Artık yeni job gelmeyecek.
	close(jobs)

	// --------------------------------------------------------
	// WORKERLARIN BİTMESİNİ BEKLE
	// --------------------------------------------------------

	wg.Wait()

	// Bütün workerlar bittiğine göre
	// artık result gönderilmeyecek.
	close(results)

	// --------------------------------------------------------
	// RESULTS CONSUMER
	// --------------------------------------------------------

	fmt.Println()
	fmt.Println("RESULTS")
	fmt.Println("------------------------------------")

	completed := 0

	for result := range results {

		completed++

		fmt.Printf(
			"Worker %d -> Task %d (%s) -> %s\n",
			result.WorkerID,
			result.TaskID,
			result.TaskName,
			strings.ToUpper(result.Status),
		)
	}

	fmt.Println("------------------------------------")
	fmt.Println("Completed tasks:", completed)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	fmt.Println()
	fmt.Println("TASK PROCESSING CENTER")
	fmt.Println()

	// --------------------------------------------------------
	// TASK 1
	// Unbuffered channels
	// --------------------------------------------------------

	demoUnbufferedChannel()

	// --------------------------------------------------------
	// TASK 2
	// Buffered channels
	// --------------------------------------------------------

	demoBufferedChannel()

	// --------------------------------------------------------
	// TASK 3
	// Close + range
	// --------------------------------------------------------

	demoCloseAndRange()

	// --------------------------------------------------------
	// TASK 4
	// Worker pattern
	// --------------------------------------------------------

	runWorkerPool()

	fmt.Println()
	fmt.Println("All Day 17 tasks completed.")
}
