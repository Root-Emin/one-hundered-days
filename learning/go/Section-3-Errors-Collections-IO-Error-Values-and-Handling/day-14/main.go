package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const tasksFile = "tasks.json"

// --------------------------------------------------
// MODEL
// --------------------------------------------------

type Task struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Done        bool   `json:"done"`
	Description string `json:"description,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
}

// --------------------------------------------------
// TASK MANAGER
// --------------------------------------------------

type TaskManager struct {
	Tasks []Task `json:"tasks"`
}

func NewTaskManager() *TaskManager {
	return &TaskManager{
		Tasks: make([]Task, 0),
	}
}

func (tm *TaskManager) Add(
	title string,
	description string,
	priority *int,
) {
	task := Task{
		ID:          len(tm.Tasks) + 1,
		Title:       title,
		Done:        false,
		Description: description,
		Priority:    priority,
	}

	tm.Tasks = append(tm.Tasks, task)
}

func (tm *TaskManager) MarkAsDone(id int) error {
	for i := range tm.Tasks {
		if tm.Tasks[i].ID == id {
			tm.Tasks[i].Done = true
			return nil
		}
	}

	return fmt.Errorf("task %d not found", id)
}

// --------------------------------------------------
// TASK 1
// MARSHAL STRUCTS
// --------------------------------------------------

// TaskManager'ı JSON'a çevirir.
func MarshalTasks(manager *TaskManager) ([]byte, error) {
	data, err := json.MarshalIndent(
		manager,
		"",
		"  ",
	)

	if err != nil {
		return nil, fmt.Errorf(
			"could not marshal tasks: %w",
			err,
		)
	}

	return data, nil
}

// --------------------------------------------------
// TASK 2
// UNMARSHAL JSON
// --------------------------------------------------

// JSON verisini TaskManager'a çevirir.
func UnmarshalTasks(data []byte) (*TaskManager, error) {
	var manager TaskManager

	err := json.Unmarshal(
		data,
		&manager,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"could not unmarshal tasks: %w",
			err,
		)
	}

	return &manager, nil
}

// --------------------------------------------------
// TASK 3
// JSON ENCODER
// --------------------------------------------------

// io.Writer'a JSON yazar.
func EncodeTasks(
	w io.Writer,
	manager *TaskManager,
) error {
	encoder := json.NewEncoder(w)

	// JSON'u daha okunabilir hale getiriyoruz.
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manager); err != nil {
		return fmt.Errorf(
			"could not encode tasks: %w",
			err,
		)
	}

	return nil
}

// --------------------------------------------------
// TASK 3
// JSON DECODER
// --------------------------------------------------

// io.Reader'dan JSON okuyup TaskManager'a çevirir.
func DecodeTasks(
	r io.Reader,
) (*TaskManager, error) {
	var manager TaskManager

	decoder := json.NewDecoder(r)

	if err := decoder.Decode(&manager); err != nil {
		return nil, fmt.Errorf(
			"could not decode tasks: %w",
			err,
		)
	}

	return &manager, nil
}

// --------------------------------------------------
// FILE OPERATIONS
// --------------------------------------------------

// JSON'u dosyaya Marshal + os.WriteFile ile kaydeder.
func SaveWithMarshal(
	filename string,
	manager *TaskManager,
) error {
	data, err := MarshalTasks(manager)

	if err != nil {
		return fmt.Errorf(
			"could not prepare task JSON: %w",
			err,
		)
	}

	if err := os.WriteFile(
		filename,
		data,
		0644,
	); err != nil {
		return fmt.Errorf(
			"could not write task file: %w",
			err,
		)
	}

	return nil
}

// Encoder kullanarak JSON'u doğrudan dosyaya yazar.
func SaveWithEncoder(
	filename string,
	manager *TaskManager,
) error {
	file, err := os.Create(filename)

	if err != nil {
		return fmt.Errorf(
			"could not create task file: %w",
			err,
		)
	}

	defer file.Close()

	if err := EncodeTasks(
		file,
		manager,
	); err != nil {
		return fmt.Errorf(
			"could not encode tasks into file: %w",
			err,
		)
	}

	return nil
}

// --------------------------------------------------
// LOAD
// --------------------------------------------------

// os.ReadFile + Unmarshal
func LoadWithUnmarshal(
	filename string,
) (*TaskManager, error) {
	data, err := os.ReadFile(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"task file does not exist: %w",
				err,
			)
		}

		return nil, fmt.Errorf(
			"could not read task file: %w",
			err,
		)
	}

	manager, err := UnmarshalTasks(data)

	if err != nil {
		return nil, fmt.Errorf(
			"could not parse task file: %w",
			err,
		)
	}

	return manager, nil
}

// os.File + Decoder
func LoadWithDecoder(
	filename string,
) (*TaskManager, error) {
	file, err := os.Open(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"task file does not exist: %w",
				err,
			)
		}

		return nil, fmt.Errorf(
			"could not open task file: %w",
			err,
		)
	}

	defer file.Close()

	manager, err := DecodeTasks(file)

	if err != nil {
		return nil, fmt.Errorf(
			"could not decode task file: %w",
			err,
		)
	}

	return manager, nil
}

// --------------------------------------------------
// OPTIONAL FIELD DEMO
// --------------------------------------------------

func intPtr(value int) *int {
	return &value
}

// --------------------------------------------------
// DISPLAY
// --------------------------------------------------

func PrintTasks(manager *TaskManager) {
	fmt.Println("\n--- Tasks ---")

	for _, task := range manager.Tasks {
		status := "TODO"

		if task.Done {
			status = "DONE"
		}

		fmt.Printf(
			"%d. [%s] %s",
			task.ID,
			status,
			task.Title,
		)

		if task.Description != "" {
			fmt.Printf(
				" — %s",
				task.Description,
			)
		}

		if task.Priority != nil {
			fmt.Printf(
				" [priority: %d]",
				*task.Priority,
			)
		}

		fmt.Println()
	}
}

// --------------------------------------------------
// MAIN
// --------------------------------------------------

func main() {
	fmt.Println("=== Go JSON Task Manager ===")

	// ------------------------------------------------
	// 1. Yeni TaskManager oluştur
	// ------------------------------------------------

	manager := NewTaskManager()

	// Priority değerleri pointer olarak tutuluyor.
	highPriority := 1
	lowPriority := 3

	manager.Add(
		"Learn JSON",
		"Study Marshal and Unmarshal",
		intPtr(highPriority),
	)

	manager.Add(
		"Build API",
		"",
		nil,
	)

	manager.Add(
		"Write tests",
		"Test JSON decoding",
		intPtr(lowPriority),
	)

	// Bir task'ı tamamlayalım.
	if err := manager.MarkAsDone(1); err != nil {
		fmt.Println("Error:", err)
		return
	}

	PrintTasks(manager)

	// ------------------------------------------------
	// 2. TASK 1
	// json.Marshal
	// ------------------------------------------------

	fmt.Println("\n--- Marshal ---")

	data, err := MarshalTasks(manager)

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println(string(data))

	// ------------------------------------------------
	// 3. os.WriteFile
	// ------------------------------------------------

	fmt.Println("\n--- Save JSON with os.WriteFile ---")

	if err := SaveWithMarshal(
		tasksFile,
		manager,
	); err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println(
		"tasks.json successfully written.",
	)

	// ------------------------------------------------
	// 4. TASK 2
	// json.Unmarshal
	// ------------------------------------------------

	fmt.Println("\n--- Unmarshal ---")

	loadedManager, err := LoadWithUnmarshal(
		tasksFile,
	)

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	PrintTasks(loadedManager)

	// ------------------------------------------------
	// 5. TASK 3
	// json.Encoder
	// ------------------------------------------------

	fmt.Println("\n--- Encoder ---")

	if err := SaveWithEncoder(
		tasksFile,
		loadedManager,
	); err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println(
		"Tasks saved using json.Encoder.",
	)

	// ------------------------------------------------
	// 6. TASK 3
	// json.Decoder
	// ------------------------------------------------

	fmt.Println("\n--- Decoder ---")

	decodedManager, err := LoadWithDecoder(
		tasksFile,
	)

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	PrintTasks(decodedManager)

	// ------------------------------------------------
	// 7. UNKNOWN JSON FIELD
	// ------------------------------------------------

	fmt.Println("\n--- Unknown JSON Field ---")

	jsonWithUnknownField := []byte(`
{
	"tasks": [
		{
			"id": 100,
			"title": "Future Task",
			"done": false,
			"unknown_field": "this is ignored"
		}
	]
}
`)

	var futureManager TaskManager

	if err := json.Unmarshal(
		jsonWithUnknownField,
		&futureManager,
	); err != nil {
		fmt.Println("Error:", err)
		return
	}

	PrintTasks(&futureManager)

	// ------------------------------------------------
	// 8. OPTIONAL FIELD
	// ------------------------------------------------

	fmt.Println("\n--- Optional Fields ---")

	optionalPriority := 0

	taskWithZeroPriority := Task{
		ID:       200,
		Title:    "Priority Zero",
		Priority: &optionalPriority,
	}

	taskWithoutPriority := Task{
		ID:       201,
		Title:    "No Priority",
		Priority: nil,
	}

	firstJSON, err := json.MarshalIndent(
		taskWithZeroPriority,
		"",
		"  ",
	)

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	secondJSON, err := json.MarshalIndent(
		taskWithoutPriority,
		"",
		"  ",
	)

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Priority = 0:")
	fmt.Println(string(firstJSON))

	fmt.Println("\nPriority = nil:")
	fmt.Println(string(secondJSON))

	// ------------------------------------------------
	// 9. ERROR HANDLING
	// ------------------------------------------------

	fmt.Println("\n--- Error Handling ---")

	_, err = LoadWithUnmarshal(
		"does-not-exist.json",
	)

	if err != nil {
		fmt.Println("Error:")
		fmt.Println(err)

		if errors.Is(err, os.ErrNotExist) {
			fmt.Println(
				"Confirmed: task file does not exist.",
			)
		}
	}

	fmt.Println("\n=== Finished ===")
}
