package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// ============================================================
// DOMAIN
// ============================================================

type DownloadResult struct {
	URL        string
	StatusCode int
	Body       string
	Err        error
}

// ============================================================
// SAFE SHARED STATE
// ============================================================

// Birden fazla goroutine aynı Report nesnesine yazabilir.
// Mutex sayesinde data race oluşmasını engelliyoruz.
type Report struct {
	mu      sync.Mutex
	results []DownloadResult
}

func (r *Report) Add(result DownloadResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.results = append(r.results, result)
}

func (r *Report) Snapshot() []DownloadResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	// İç slice'ın dışarıdan değiştirilmesini engellemek için
	// kopyasını döndürüyoruz.
	results := make([]DownloadResult, len(r.results))
	copy(results, r.results)

	return results
}

// ============================================================
// CONCURRENT DOWNLOADER
// ============================================================

func concurrentDownload(
	client *http.Client,
	urls []string,
	report *Report,
) {
	var wg sync.WaitGroup

	for _, url := range urls {
		wg.Add(1)

		go func(url string) {
			defer wg.Done()

			result := download(client, url)

			// Shared state'e güvenli şekilde yazıyoruz.
			report.Add(result)

		}(url)
	}

	// Bütün downloader goroutine'lerinin bitmesini bekle.
	wg.Wait()
}

func download(client *http.Client, url string) DownloadResult {
	response, err := client.Get(url)

	if err != nil {
		return DownloadResult{
			URL: url,
			Err: err,
		}
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)

	if err != nil {
		return DownloadResult{
			URL:        url,
			StatusCode: response.StatusCode,
			Err:        err,
		}
	}

	return DownloadResult{
		URL:        url,
		StatusCode: response.StatusCode,
		Body:       string(body),
	}
}

// ============================================================
// PIPELINE
// ============================================================

// Stage 1
// Sayıları üretir.
func generateNumbers(numbers ...int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for _, number := range numbers {
			out <- number
		}
	}()

	return out
}

// Stage 2
// Gelen sayıların karesini alır.
func squareNumbers(in <-chan int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for number := range in {
			out <- number * number
		}
	}()

	return out
}

// Stage 3
// Sonuçları yazdırır.
func printResults(in <-chan int) {
	for result := range in {
		fmt.Println("Pipeline result:", result)
	}
}

// ============================================================
// TIMEOUT FETCH
// ============================================================

func fetchWithTimeout(
	client *http.Client,
	url string,
	timeout time.Duration,
) DownloadResult {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)

	if err != nil {
		return DownloadResult{
			URL: url,
			Err: err,
		}
	}

	resultCh := make(chan DownloadResult, 1)

	go func() {

		response, err := client.Do(request)

		if err != nil {
			resultCh <- DownloadResult{
				URL: url,
				Err: err,
			}

			return
		}

		defer response.Body.Close()

		body, err := io.ReadAll(response.Body)

		resultCh <- DownloadResult{
			URL:        url,
			StatusCode: response.StatusCode,
			Body:       string(body),
			Err:        err,
		}

	}()

	select {

	case result := <-resultCh:

		return result

	case <-time.After(timeout):

		// Timeout olduğunda request'i iptal ediyoruz.
		cancel()

		return DownloadResult{
			URL: url,
			Err: fmt.Errorf("request timeout after %s", timeout),
		}
	}
}

// ============================================================
// TEST SERVER
// ============================================================

// Gerçek internete bağlı kalmadan network davranışını
// simüle etmek için local HTTP server oluşturuyoruz.
func createTestServer() *httptest.Server {

	mux := http.NewServeMux()

	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {

		fmt.Fprint(w, `{"users":3}`)
	})

	mux.HandleFunc("/products", func(w http.ResponseWriter, r *http.Request) {

		fmt.Fprint(w, `{"products":10}`)
	})

	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {

		fmt.Fprint(w, `{"orders":25}`)
	})

	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {

		// Timeout test etmek için endpoint'i yavaşlatıyoruz.
		time.Sleep(2 * time.Second)

		fmt.Fprint(w, `{"status":"slow response"}`)
	})

	return httptest.NewServer(mux)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	server := createTestServer()
	defer server.Close()

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// ========================================================
	// TASK 1
	// CONCURRENT DOWNLOADER
	// ========================================================

	fmt.Println("========================================")
	fmt.Println("TASK 1 - CONCURRENT DOWNLOADER")
	fmt.Println("========================================")

	urls := []string{
		server.URL + "/users",
		server.URL + "/products",
		server.URL + "/orders",
	}

	report := &Report{}

	// Üç URL aynı anda fetch edilecek.
	concurrentDownload(
		client,
		urls,
		report,
	)

	results := report.Snapshot()

	for _, result := range results {

		if result.Err != nil {

			fmt.Println(
				"ERROR:",
				result.URL,
				result.Err,
			)

			continue
		}

		fmt.Println(
			"SUCCESS:",
			result.URL,
			"status:",
			result.StatusCode,
			"body:",
			result.Body,
		)
	}

	// ========================================================
	// TASK 2
	// PIPELINE
	// ========================================================

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("TASK 2 - PIPELINE")
	fmt.Println("========================================")

	// Stage 1
	numbers := generateNumbers(
		1,
		2,
		3,
		4,
		5,
	)

	// Stage 2
	squaredNumbers := squareNumbers(numbers)

	// Stage 3
	printResults(squaredNumbers)

	// ========================================================
	// TASK 3
	// TIMEOUT FETCH
	// ========================================================

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("TASK 3 - TIMEOUT FETCH")
	fmt.Println("========================================")

	timeoutResult := fetchWithTimeout(
		client,
		server.URL+"/slow",
		500*time.Millisecond,
	)

	if timeoutResult.Err != nil {

		fmt.Println(
			"Timeout fetch error:",
			timeoutResult.Err,
		)

	} else {

		fmt.Println(
			"Timeout fetch success:",
			timeoutResult.Body,
		)
	}

	// ========================================================
	// TASK 4
	// RACE CHECK
	// ========================================================

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("TASK 4 - RACE SAFE SHARED STATE")
	fmt.Println("========================================")

	fmt.Println(
		"Total downloaded results:",
		len(report.Snapshot()),
	)

	fmt.Println()
	fmt.Println("All concurrency tasks completed.")
}
