package task

import (
	"errors"
	"strings"
)

var (
	ErrEmptyTitle      = errors.New("task title cannot be empty")
	ErrTaskNotFound    = errors.New("task not found")
	ErrAlreadyComplete = errors.New("task is already completed")
)

type Task struct {
	ID    int
	Title string
	Done  bool
}

type TaskService struct {
	tasks  map[int]Task
	nextID int
}

func NewTaskService() *TaskService {
	return &TaskService{
		tasks:  make(map[int]Task),
		nextID: 1,
	}
}

func (s *TaskService) CreateTask(title string) (Task, error) {

	title = strings.TrimSpace(title)

	if title == "" {
		return Task{}, ErrEmptyTitle
	}

	task := Task{
		ID:    s.nextID,
		Title: title,
		Done:  false,
	}

	s.tasks[task.ID] = task
	s.nextID++

	return task, nil
}

func (s *TaskService) GetTask(id int) (Task, error) {

	task, exists := s.tasks[id]

	if !exists {
		return Task{}, ErrTaskNotFound
	}

	return task, nil
}

func (s *TaskService) CompleteTask(id int) (Task, error) {

	task, exists := s.tasks[id]

	if !exists {
		return Task{}, ErrTaskNotFound
	}

	if task.Done {
		return Task{}, ErrAlreadyComplete
	}

	task.Done = true

	s.tasks[id] = task

	return task, nil
}
