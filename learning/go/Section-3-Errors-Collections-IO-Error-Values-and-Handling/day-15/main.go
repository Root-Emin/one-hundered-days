package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ============================================================
// CONFIG
// ============================================================

type Config struct {
	InputFile     string `json:"input_file"`
	OutputFile    string `json:"output_file"`
	MinWordLength int    `json:"min_word_length"`
}

// Validate config'in kullanılabilir olup olmadığını kontrol eder.
func (c Config) Validate() error {
	if strings.TrimSpace(c.InputFile) == "" {
		return errors.New("input_file is required")
	}

	if strings.TrimSpace(c.OutputFile) == "" {
		return errors.New("output_file is required")
	}

	if c.MinWordLength < 1 {
		return errors.New("min_word_length must be at least 1")
	}

	return nil
}

// ============================================================
// CONFIG LOADER
// TASK 1
//
// Read a JSON config file into a struct
// and validate required fields.
// ============================================================

func LoadConfig(filename string) (Config, error) {
	file, err := os.Open(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf(
				"config file does not exist: %w",
				err,
			)
		}

		return Config{}, fmt.Errorf(
			"could not open config file: %w",
			err,
		)
	}

	defer file.Close()

	var config Config

	decoder := json.NewDecoder(file)

	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf(
			"could not decode config JSON: %w",
			err,
		)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf(
			"invalid config: %w",
			err,
		)
	}

	return config, nil
}

// ============================================================
// WORD COUNTER
// TASK 2
//
// Read a text file and count word frequencies
// using map[string]int.
// ============================================================

type WordCounter struct {
	Frequencies map[string]int
	TotalWords  int
}

// NewWordCounter yeni bir WordCounter oluşturur.
func NewWordCounter() *WordCounter {
	return &WordCounter{
		Frequencies: make(map[string]int),
	}
}

// CountReader bir io.Reader'dan kelimeleri okur.
//
// Burada dosyaya doğrudan bağımlı değiliz.
// Herhangi bir io.Reader kullanılabilir.
func (wc *WordCounter) CountReader(
	reader io.Reader,
	minWordLength int,
) error {
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := scanner.Text()

		words := strings.FieldsFunc(
			line,
			func(r rune) bool {
				return !unicode.IsLetter(r) &&
					!unicode.IsNumber(r)
			},
		)

		for _, word := range words {
			word = strings.ToLower(word)

			if len([]rune(word)) < minWordLength {
				continue
			}

			wc.Frequencies[word]++
			wc.TotalWords++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf(
			"could not scan input: %w",
			err,
		)
	}

	return nil
}

// CountFile dosyayı açar ve WordCounter'a verir.
func (wc *WordCounter) CountFile(
	filename string,
	minWordLength int,
) error {
	file, err := os.Open(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"input file does not exist: %w",
				err,
			)
		}

		return fmt.Errorf(
			"could not open input file: %w",
			err,
		)
	}

	defer file.Close()

	// Empty input kontrolü.
	info, err := file.Stat()

	if err != nil {
		return fmt.Errorf(
			"could not inspect input file: %w",
			err,
		)
	}

	if info.Size() == 0 {
		return errors.New("input file is empty")
	}

	if err := wc.CountReader(
		file,
		minWordLength,
	); err != nil {
		return fmt.Errorf(
			"could not count words: %w",
			err,
		)
	}

	if wc.TotalWords == 0 {
		return errors.New(
			"input contains no words matching the configuration",
		)
	}

	return nil
}

// ============================================================
// SUMMARY
// ============================================================

type Summary struct {
	TotalWords  int            `json:"total_words"`
	UniqueWords int            `json:"unique_words"`
	Frequencies map[string]int `json:"frequencies"`
}

// CreateSummary WordCounter'dan JSON'a çevrilecek
// Summary oluşturur.
func CreateSummary(
	counter *WordCounter,
) Summary {
	return Summary{
		TotalWords:  counter.TotalWords,
		UniqueWords: len(counter.Frequencies),
		Frequencies: counter.Frequencies,
	}
}

// ============================================================
// SUMMARY WRITER
// TASK 4
//
// Write a JSON summary file.
// ============================================================

func WriteSummary(
	filename string,
	summary Summary,
) error {
	file, err := os.Create(filename)

	if err != nil {
		return fmt.Errorf(
			"could not create summary file: %w",
			err,
		)
	}

	defer file.Close()

	encoder := json.NewEncoder(file)

	encoder.SetIndent("", "  ")

	if err := encoder.Encode(summary); err != nil {
		return fmt.Errorf(
			"could not encode summary: %w",
			err,
		)
	}

	return nil
}

// ============================================================
// TASK 3
// ERROR PATH TESTS
//
// Missing files
// Malformed JSON
// Empty input
// ============================================================

func runErrorPathTests() error {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("ERROR PATH TESTS")
	fmt.Println("========================================")

	tempDir, err := os.MkdirTemp(
		"",
		"day15-tests-*",
	)

	if err != nil {
		return fmt.Errorf(
			"could not create test directory: %w",
			err,
		)
	}

	defer os.RemoveAll(tempDir)

	// --------------------------------------------------------
	// TEST 1
	// Missing config file
	// --------------------------------------------------------

	fmt.Println("\n[TEST 1] Missing config file")

	_, err = LoadConfig(
		filepath.Join(
			tempDir,
			"missing-config.json",
		),
	)

	if err == nil {
		return errors.New(
			"TEST 1 FAILED: expected missing file error",
		)
	}

	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"TEST 1 FAILED: expected os.ErrNotExist, got: %w",
			err,
		)
	}

	fmt.Println("PASS")

	// --------------------------------------------------------
	// TEST 2
	// Malformed JSON
	// --------------------------------------------------------

	fmt.Println("\n[TEST 2] Malformed JSON")

	malformedConfig := filepath.Join(
		tempDir,
		"malformed.json",
	)

	err = os.WriteFile(
		malformedConfig,
		[]byte(`{
			"input_file": "input.txt",
			"output_file":
		}`),
		0644,
	)

	if err != nil {
		return fmt.Errorf(
			"could not create malformed config: %w",
			err,
		)
	}

	_, err = LoadConfig(malformedConfig)

	if err == nil {
		return errors.New(
			"TEST 2 FAILED: expected malformed JSON error",
		)
	}

	fmt.Println("PASS")

	// --------------------------------------------------------
	// TEST 3
	// Empty input
	// --------------------------------------------------------

	fmt.Println("\n[TEST 3] Empty input")

	emptyFile := filepath.Join(
		tempDir,
		"empty.txt",
	)

	err = os.WriteFile(
		emptyFile,
		[]byte{},
		0644,
	)

	if err != nil {
		return fmt.Errorf(
			"could not create empty file: %w",
			err,
		)
	}

	counter := NewWordCounter()

	err = counter.CountFile(
		emptyFile,
		3,
	)

	if err == nil {
		return errors.New(
			"TEST 3 FAILED: expected empty input error",
		)
	}

	fmt.Println("PASS")

	fmt.Println("\nAll error path tests passed.")

	return nil
}

// ============================================================
// DEMO FILE CREATION
// ============================================================

// createDemoFiles programı ilk kez çalıştırırken
// ihtiyaç duyduğu örnek dosyaları oluşturur.
func createDemoFiles() error {
	configExists := true

	if _, err := os.Stat("config.json"); err != nil {
		if os.IsNotExist(err) {
			configExists = false
		} else {
			return fmt.Errorf(
				"could not inspect config.json: %w",
				err,
			)
		}
	}

	if !configExists {
		configJSON := `{
  "input_file": "input.txt",
  "output_file": "summary.json",
  "min_word_length": 3
}
`

		if err := os.WriteFile(
			"config.json",
			[]byte(configJSON),
			0644,
		); err != nil {
			return fmt.Errorf(
				"could not create demo config: %w",
				err,
			)
		}

		fmt.Println("Created config.json")
	}

	inputExists := true

	if _, err := os.Stat("input.txt"); err != nil {
		if os.IsNotExist(err) {
			inputExists = false
		} else {
			return fmt.Errorf(
				"could not inspect input.txt: %w",
				err,
			)
		}
	}

	if !inputExists {
		input := `Go is simple.
Go is fast.
Go is powerful.
Writing Go code is fun.
`

		if err := os.WriteFile(
			"input.txt",
			[]byte(input),
			0644,
		); err != nil {
			return fmt.Errorf(
				"could not create demo input: %w",
				err,
			)
		}

		fmt.Println("Created input.txt")
	}

	return nil
}

// ============================================================
// MAIN CLI
// TASK 4
//
// Load config
// Read data
// Count words
// Write JSON summary
// ============================================================

func main() {
	fmt.Println("========================================")
	fmt.Println("       DAY 15 - WORD ANALYZER")
	fmt.Println("========================================")

	// --------------------------------------------------------
	// 1. Demo dosyalarını hazırla
	// --------------------------------------------------------

	if err := createDemoFiles(); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// --------------------------------------------------------
	// 2. CONFIG LOADER
	// --------------------------------------------------------

	fmt.Println("\nLoading configuration...")

	config, err := LoadConfig("config.json")

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Configuration loaded successfully.")

	fmt.Printf(
		"Input file: %s\n",
		config.InputFile,
	)

	fmt.Printf(
		"Output file: %s\n",
		config.OutputFile,
	)

	fmt.Printf(
		"Minimum word length: %d\n",
		config.MinWordLength,
	)

	// --------------------------------------------------------
	// 3. WORD COUNTER
	// --------------------------------------------------------

	fmt.Println("\nReading input file...")

	counter := NewWordCounter()

	if err := counter.CountFile(
		config.InputFile,
		config.MinWordLength,
	); err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Input processed successfully.")

	// --------------------------------------------------------
	// 4. SUMMARY OLUŞTUR
	// --------------------------------------------------------

	summary := CreateSummary(counter)

	fmt.Println("\n========================================")
	fmt.Println("RESULT")
	fmt.Println("========================================")

	fmt.Printf(
		"Total words: %d\n",
		summary.TotalWords,
	)

	fmt.Printf(
		"Unique words: %d\n",
		summary.UniqueWords,
	)

	fmt.Println("\nWord frequencies:")

	for word, count := range summary.Frequencies {
		fmt.Printf(
			"  %-15s %d\n",
			word,
			count,
		)
	}

	// --------------------------------------------------------
	// 5. JSON SUMMARY YAZ
	// --------------------------------------------------------

	fmt.Println("\nWriting summary...")

	if err := WriteSummary(
		config.OutputFile,
		summary,
	); err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Printf(
		"Summary written to %s\n",
		config.OutputFile,
	)

	// --------------------------------------------------------
	// 6. ERROR PATH TESTS
	// --------------------------------------------------------

	if err := runErrorPathTests(); err != nil {
		fmt.Println("\nError path test failure:")
		fmt.Println(err)
		return
	}

	fmt.Println("\n========================================")
	fmt.Println("DAY 15 COMPLETED")
	fmt.Println("========================================")
}
