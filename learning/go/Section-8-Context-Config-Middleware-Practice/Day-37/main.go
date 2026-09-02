package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// ============================================================
// CONFIG
//
// Environment variable'ların tamamını tek bir typed struct
// içerisinde topluyoruz.
//
// Böylece uygulamanın geri kalanında:
//
// os.Getenv("PORT")
// os.Getenv("DATABASE_URL")
//
// gibi dağınık environment okumaları yapmıyoruz.
// ============================================================

type Config struct {
	Port        string
	DatabaseURL string
	AppEnv      string
}

// ============================================================
// LOAD CONFIG
//
// Önce local .env dosyasını yüklemeyi dener.
// Ardından gerçek OS environment variable'larını okur.
//
// OS environment variable'ları .env değerlerinin üzerine
// yazabilir.
//
// Bu önemli çünkü production'da .env kullanmak zorunda değiliz.
// ============================================================

func LoadConfig() (Config, error) {
	// --------------------------------------------------------
	// Local development için .env yüklemeyi deniyoruz.
	//
	// .env yoksa hata vermiyoruz.
	// Çünkü production ortamında .env bulunmayabilir.
	// --------------------------------------------------------

	if err := loadDotEnv(".env"); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf(
				"load .env: %w",
				err,
			)
		}
	}

	// --------------------------------------------------------
	// PORT
	//
	// Optional.
	// Verilmezse 8080 kullanıyoruz.
	// --------------------------------------------------------

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	// --------------------------------------------------------
	// DATABASE_URL
	//
	// Required.
	// Çünkü uygulamanın database bağlantısına ihtiyaç duyduğunu
	// varsayıyoruz.
	// --------------------------------------------------------

	databaseURL := os.Getenv("DATABASE_URL")

	// --------------------------------------------------------
	// APP_ENV
	//
	// Optional.
	// Default: development
	// --------------------------------------------------------

	appEnv := os.Getenv("APP_ENV")

	if appEnv == "" {
		appEnv = "development"
	}

	config := Config{
		Port:        port,
		DatabaseURL: databaseURL,
		AppEnv:      appEnv,
	}

	// --------------------------------------------------------
	// Startup validation
	// --------------------------------------------------------

	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// ============================================================
// CONFIG VALIDATION
//
// Fail Fast yaklaşımı:
//
// Uygulama yanlış config ile çalışmaya başlamıyor.
// ============================================================

func (c Config) Validate() error {
	// --------------------------------------------------------
	// PORT validation
	// --------------------------------------------------------

	port, err := strconv.Atoi(c.Port)

	if err != nil {
		return fmt.Errorf(
			"invalid PORT %q: must be a number",
			c.Port,
		)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf(
			"invalid PORT %q: must be between 1 and 65535",
			c.Port,
		)
	}

	// --------------------------------------------------------
	// DATABASE_URL validation
	// --------------------------------------------------------

	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New(
			"DATABASE_URL is required",
		)
	}

	// --------------------------------------------------------
	// APP_ENV validation
	// --------------------------------------------------------

	switch c.AppEnv {
	case "development", "staging", "production":
		// valid
	default:
		return fmt.Errorf(
			"invalid APP_ENV %q: must be development, staging, or production",
			c.AppEnv,
		)
	}

	return nil
}

// ============================================================
// .ENV LOADER
//
// Basit bir local development loader.
//
// Desteklenen format:
//
// PORT=8080
// DATABASE_URL=postgres://localhost/myapp
// APP_ENV=development
//
// Ayrıca:
//
// # comment
// export PORT=8080
// KEY="value"
// KEY='value'
//
// gibi basit kullanımları da destekliyoruz.
// ============================================================

func loadDotEnv(filename string) error {
	file, err := os.Open(filename)

	if err != nil {
		return err
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)

	lineNumber := 0

	for scanner.Scan() {
		lineNumber++

		line := strings.TrimSpace(scanner.Text())

		// ----------------------------------------------------
		// Empty line
		// ----------------------------------------------------

		if line == "" {
			continue
		}

		// ----------------------------------------------------
		// Comment
		// ----------------------------------------------------

		if strings.HasPrefix(line, "#") {
			continue
		}

		// ----------------------------------------------------
		// Optional "export"
		//
		// export PORT=8080
		// ----------------------------------------------------

		line = strings.TrimSpace(
			strings.TrimPrefix(line, "export "),
		)

		// ----------------------------------------------------
		// KEY=VALUE ayır
		// ----------------------------------------------------

		parts := strings.SplitN(line, "=", 2)

		if len(parts) != 2 {
			return fmt.Errorf(
				"invalid .env syntax on line %d",
				lineNumber,
			)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return fmt.Errorf(
				"empty environment variable name on line %d",
				lineNumber,
			)
		}

		// ----------------------------------------------------
		// Quotes kaldır
		// ----------------------------------------------------

		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// ----------------------------------------------------
		// Environment variable'ı set et.
		//
		// Burada os.Setenv kullanıyoruz.
		// Sonrasında os.Getenv ile okuyacağız.
		// ----------------------------------------------------

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf(
				"set environment variable %q: %w",
				key,
				err,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf(
			"read .env: %w",
			err,
		)
	}

	return nil
}

// ============================================================
// APPLICATION
// ============================================================

type Application struct {
	Config Config
}

// ============================================================
// HEALTH HANDLER
//
// Config'in gerçekten uygulamaya geçtiğini görmek için basit
// bir health endpoint.
//
// GET /health
// ============================================================

func (app *Application) healthHandler(
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

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	fmt.Fprintf(
		w,
		`{"status":"ok","environment":%q}`,
		app.Config.AppEnv,
	)
}

// ============================================================
// ROOT HANDLER
// ============================================================

func (app *Application) rootHandler(
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

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	fmt.Fprintln(
		w,
		"Day 37 API is running",
	)
}

// ============================================================
// REQUEST LOGGING MIDDLEWARE
//
// Günün ana konusu config olsa da başlık Context, Config &
// Middleware olduğu için middleware yapısını da server'a
// bağlıyoruz.
//
// Middleware:
//
// request
//    ↓
// loggingMiddleware
//    ↓
// handler
// ============================================================

func loggingMiddleware(
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(
		func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			log.Printf(
				"%s %s",
				r.Method,
				r.URL.Path,
			)

			next.ServeHTTP(w, r)
		},
	)
}

// ============================================================
// ROUTER
// ============================================================

func (app *Application) router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(
		"/",
		app.rootHandler,
	)

	mux.HandleFunc(
		"/health",
		app.healthHandler,
	)

	return loggingMiddleware(mux)
}

// ============================================================
// SERVER
// ============================================================

func NewServer(app *Application) *http.Server {
	return &http.Server{
		Addr:    ":" + app.Config.Port,
		Handler: app.router(),
	}
}

// ============================================================
// MAIN
//
// Startup flow:
//
// 1. Load .env
// 2. Read environment variables
// 3. Build Config
// 4. Validate Config
// 5. Fail fast if invalid
// 6. Create application
// 7. Start HTTP server
// ============================================================

func main() {
	// --------------------------------------------------------
	// CONFIG
	// --------------------------------------------------------

	config, err := LoadConfig()

	if err != nil {
		log.Fatalf(
			"configuration error: %v",
			err,
		)
	}

	// --------------------------------------------------------
	// IMPORTANT:
	// DATABASE_URL secret olduğu için tamamını loglamıyoruz.
	// --------------------------------------------------------

	log.Printf(
		"configuration loaded: env=%s port=%s",
		config.AppEnv,
		config.Port,
	)

	// --------------------------------------------------------
	// APPLICATION
	// --------------------------------------------------------

	app := &Application{
		Config: config,
	}

	// --------------------------------------------------------
	// HTTP SERVER
	// --------------------------------------------------------

	server := NewServer(app)

	log.Printf(
		"server starting on :%s",
		config.Port,
	)

	log.Printf(
		"environment: %s",
		config.AppEnv,
	)

	// --------------------------------------------------------
	// START
	// --------------------------------------------------------

	if err := server.ListenAndServe(); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatalf(
			"server failed: %v",
			err,
		)
	}
}

// ============================================================
// DAY 37 TASK CHECKLIST
//
// ✓ Read env vars
//   os.Getenv()
//   PORT
//   DATABASE_URL
//   APP_ENV
//
// ✓ Sensible defaults
//   PORT      -> 8080
//   APP_ENV   -> development
//
// ✓ Support .env locally
//   loadDotEnv()
//   .env parsing
//   comments
//   export syntax
//   quoted values
//
// ✓ Validate on startup
//   PORT validation
//   DATABASE_URL required
//   APP_ENV validation
//
// ✓ Fail fast
//   log.Fatalf() on invalid configuration
//
// ✓ Struct config
//   Config struct
//   typed configuration
//
// ✓ Middleware
//   loggingMiddleware()
//
// ✓ HTTP server uses Config
//   Addr: ":" + config.Port
//
// ✓ No secret logging
//   DATABASE_URL never printed
//
// ============================================================
//
// EXAMPLE .env
//
// PORT=8080
// DATABASE_URL=postgres://localhost/myapp
// APP_ENV=development
//
// IMPORTANT:
// .env should NOT be committed to git.
//
// Add this to .gitignore:
//
// .env
//
// ============================================================
