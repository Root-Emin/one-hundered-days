package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const tasksFile = "tasks.txt"

// Task bizim uygulamadaki temel veri modelimiz.
type Task struct {
	ID   int
	Name string
	Done bool
}

// TaskManager task'ları memory'de ve dosyada yönetiyor.
type TaskManager struct {
	Tasks []Task
}

// NewTaskManager yeni bir TaskManager oluşturur.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		Tasks: make([]Task, 0),
	}
}

// Add yeni bir task ekler.
func (tm *TaskManager) Add(name string) {
	task := Task{
		ID:   len(tm.Tasks) + 1,
		Name: name,
		Done: false,
	}

	tm.Tasks = append(tm.Tasks, task)
}

// MarkAsDone task'ı tamamlandı olarak işaretler.
func (tm *TaskManager) MarkAsDone(id int) error {
	for i := range tm.Tasks {
		if tm.Tasks[i].ID == id {
			tm.Tasks[i].Done = true
			return nil
		}
	}

	return fmt.Errorf("task %d not found", id)
}

// -------------------------
// FILE READING
// -------------------------

// ReadFileContent dosyanın tamamını os.ReadFile ile okur.
//
// Bu yöntem küçük dosyalar için uygundur.
func ReadFileContent(filename string) (string, error) {
	data, err := os.ReadFile(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"task file does not exist: %w",
				err,
			)
		}

		return "", fmt.Errorf(
			"could not read task file: %w",
			err,
		)
	}

	return string(data), nil
}

// ReadTasksFromFile dosyayı satır satır okur.
//
// Burada bufio.Scanner kullanıyoruz.
func ReadTasksFromFile(filename string) ([]Task, error) {
	file, err := os.Open(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"cannot open task file because it does not exist: %w",
				err,
			)
		}

		return nil, fmt.Errorf(
			"cannot open task file: %w",
			err,
		)
	}

	defer file.Close()

	return ReadTasksFromReader(file)
}

// ReadTasksFromReader io.Reader kabul eder.
//
// Böylece bu fonksiyon sadece dosyaya bağlı değildir.
// Herhangi bir io.Reader ile çalışabilir.
func ReadTasksFromReader(r io.Reader) ([]Task, error) {
	scanner := bufio.NewScanner(r)

	tasks := make([]Task, 0)

	id := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		tasks = append(tasks, Task{
			ID:   id,
			Name: line,
			Done: false,
		})

		id++
	}

	// Scanner'ın hata üretip üretmediğini mutlaka kontrol ediyoruz.
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf(
			"error while scanning task file: %w",
			err,
		)
	}

	return tasks, nil
}

// -------------------------
// FILE WRITING
// -------------------------

// CreateInitialFile os.WriteFile kullanarak dosya oluşturur.
//
// Dosya yoksa ilk kez burada oluşturabiliriz.
func CreateInitialFile(filename string) error {
	initialContent := []byte(
		"Learn Go interfaces\n" +
			"Build file manager\n" +
			"Write tests\n",
	)

	err := os.WriteFile(
		filename,
		initialContent,
		0644,
	)

	if err != nil {
		return fmt.Errorf(
			"could not create initial task file: %w",
			err,
		)
	}

	return nil
}

// SaveTasksToFile task'ları buffered writer ile dosyaya yazar.
func SaveTasksToFile(filename string, tasks []Task) error {
	file, err := os.Create(filename)

	if err != nil {
		return fmt.Errorf(
			"could not create task file for writing: %w",
			err,
		)
	}

	defer file.Close()

	writer := bufio.NewWriter(file)

	if err := WriteTasks(writer, tasks); err != nil {
		return fmt.Errorf(
			"could not write tasks: %w",
			err,
		)
	}

	// Buffered veriyi gerçek dosyaya gönderiyoruz.
	if err := writer.Flush(); err != nil {
		return fmt.Errorf(
			"could not flush task file: %w",
			err,
		)
	}

	return nil
}

// WriteTasks io.Writer kabul eder.
//
// Böylece task'ları doğrudan dosyaya yazmak zorunda değiliz.
func WriteTasks(w io.Writer, tasks []Task) error {
	for _, task := range tasks {
		status := "TODO"

		if task.Done {
			status = "DONE"
		}

		_, err := fmt.Fprintf(
			w,
			"[%s] %s\n",
			status,
			task.Name,
		)

		if err != nil {
			return fmt.Errorf(
				"could not write task %q: %w",
				task.Name,
				err,
			)
		}
	}

	return nil
}

// -------------------------
// REPORT
// -------------------------

// WriteSummary io.Writer kullanarak task özetini yazar.
func WriteSummary(w io.Writer, tasks []Task) error {
	total := len(tasks)
	completed := 0

	for _, task := range tasks {
		if task.Done {
			completed++
		}
	}

	pending := total - completed

	_, err := fmt.Fprintf(
		w,
		"\nTask Summary\n"+
			"------------\n"+
			"Total: %d\n"+
			"Completed: %d\n"+
			"Pending: %d\n",
		total,
		completed,
		pending,
	)

	if err != nil {
		return fmt.Errorf(
			"could not write task summary: %w",
			err,
		)
	}

	return nil
}

// -------------------------
// MAIN
// -------------------------

func main() {
	fmt.Println("=== Go Task Manager ===")

	// ---------------------------------
	// 1. DOSYA YOKSA OLUŞTUR
	// ---------------------------------

	_, err := os.ReadFile(tasksFile)

	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Task file does not exist.")
			fmt.Println("Creating initial task file...")

			if err := CreateInitialFile(tasksFile); err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
		} else {
			fmt.Printf(
				"Unexpected file error: %v\n",
				err,
			)
			return
		}
	}

	// ---------------------------------
	// 2. os.ReadFile
	// ---------------------------------

	fmt.Println("\n--- Raw File Content ---")

	content, err := ReadFileContent(tasksFile)

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Print(content)

	// ---------------------------------
	// 3. bufio.Scanner
	// ---------------------------------

	fmt.Println("--- Loading Tasks ---")

	tasks, err := ReadTasksFromFile(tasksFile)

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// ---------------------------------
	// 4. TASK MANAGER / COLLECTION
	// ---------------------------------

	manager := NewTaskManager()

	for _, task := range tasks {
		manager.Tasks = append(manager.Tasks, task)
	}

	manager.Add("Review Go error handling")
	manager.Add("Practice io.Reader and io.Writer")

	// Bir task'ı tamamlayalım.
	if err := manager.MarkAsDone(1); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// ---------------------------------
	// 5. TASKLARI GÖSTER
	// ---------------------------------

	fmt.Println("\n--- Current Tasks ---")

	for _, task := range manager.Tasks {
		status := "TODO"

		if task.Done {
			status = "DONE"
		}

		fmt.Printf(
			"%d. [%s] %s\n",
			task.ID,
			status,
			task.Name,
		)
	}

	// ---------------------------------
	// 6. io.Writer
	// ---------------------------------

	fmt.Println("\n--- Writing Tasks ---")

	if err := SaveTasksToFile(
		tasksFile,
		manager.Tasks,
	); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("Tasks successfully saved.")

	// ---------------------------------
	// 7. io.Writer → Terminal
	// ---------------------------------

	if err := WriteSummary(
		os.Stdout,
		manager.Tasks,
	); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// ---------------------------------
	// 8. ERROR WRAPPING DEMONSTRATION
	// ---------------------------------

	fmt.Println("\n--- Error Handling Demo ---")

	_, err = ReadFileContent("does-not-exist.txt")

	if err != nil {
		fmt.Println("Received error:")
		fmt.Println(err)

		// errors.Is ile error chain üzerinde
		// belirli bir error'u arayabiliriz.
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println(
				"Confirmed: the file does not exist.",
			)
		}
	}

	fmt.Println("\n=== Finished ===")
}
