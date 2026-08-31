package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// ============================================================
// DOMAIN
// ============================================================

type Notification struct {
	ID      int
	UserID  int
	Message string
	Channel string
}

type Result struct {
	NotificationID int
	Status         string
	WorkerID       int
}

type Service struct {
	notifications chan Notification
	results       chan Result

	wg sync.WaitGroup
}

// ============================================================
// PRODUCER
// ============================================================

func (s *Service) Produce(ctx context.Context, count int) {

	for i := 1; i <= count; i++ {

		notification := Notification{
			ID:      i,
			UserID:  1000 + i,
			Message: fmt.Sprintf("Notification %d", i),
			Channel: "push",
		}

		// --------------------------------------------------------
		// BACKPRESSURE
		//
		// Queue doluysa producer sonsuza kadar beklemiyor.
		// Bunun yerine notification'ı drop ediyoruz.
		// --------------------------------------------------------

		select {

		case s.notifications <- notification:
			fmt.Printf(
				"[PRODUCER] notification %d queued\n",
				notification.ID,
			)

		case <-ctx.Done():
			fmt.Println("[PRODUCER] cancellation received")
			return

		default:
			fmt.Printf(
				"[PRODUCER] notification %d DROPPED - queue full\n",
				notification.ID,
			)
		}

		// Producer'ı biraz yavaşlatıyoruz.
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Println("[PRODUCER] finished")
}

// ============================================================
// WORKER
// ============================================================

func (s *Service) Worker(ctx context.Context, workerID int) {

	defer s.wg.Done()

	fmt.Printf("[WORKER %d] started\n", workerID)

	for {

		// --------------------------------------------------------
		// SELECT
		//
		// Worker aynı anda iki farklı şeyi bekliyor:
		//
		// 1. Yeni notification
		// 2. Cancellation
		// --------------------------------------------------------

		select {

		case notification := <-s.notifications:

			fmt.Printf(
				"[WORKER %d] received notification %d\n",
				workerID,
				notification.ID,
			)

			// ----------------------------------------------------
			// TIMEOUT
			//
			// Notification processing'in maksimum süresi:
			// 1 saniye.
			// ----------------------------------------------------

			result := s.processNotification(
				ctx,
				workerID,
				notification,
			)

			// ----------------------------------------------------
			// RESULT GÖNDERME
			//
			// Result channel'a gönderirken de cancellation
			// kontrol ediyoruz.
			// ----------------------------------------------------

			select {

			case s.results <- result:

			case <-ctx.Done():
				fmt.Printf(
					"[WORKER %d] cancelled while sending result\n",
					workerID,
				)
				return
			}

		case <-ctx.Done():

			// ----------------------------------------------------
			// COOPERATIVE CANCELLATION
			// ----------------------------------------------------

			fmt.Printf(
				"[WORKER %d] cancellation received\n",
				workerID,
			)

			return
		}
	}
}

// ============================================================
// PROCESS NOTIFICATION
// ============================================================

func (s *Service) processNotification(
	ctx context.Context,
	workerID int,
	notification Notification,
) Result {

	// ------------------------------------------------------------
	// Her notification için maksimum 1 saniyelik timeout.
	// ------------------------------------------------------------

	processCtx, cancel := context.WithTimeout(
		ctx,
		1*time.Second,
	)

	defer cancel()

	// Simülasyon:
	// Bazı işler hızlı,
	// bazı işler yavaş olacak.
	processingTime := time.Duration(
		rand.Intn(1500),
	) * time.Millisecond

	timer := time.NewTimer(processingTime)
	defer timer.Stop()

	select {

	case <-timer.C:

		// İş başarıyla tamamlandı.

		fmt.Printf(
			"[WORKER %d] notification %d processed in %v\n",
			workerID,
			notification.ID,
			processingTime,
		)

		return Result{
			NotificationID: notification.ID,
			Status:         "processed",
			WorkerID:       workerID,
		}

	case <-processCtx.Done():

		// --------------------------------------------------------
		// Timeout veya parent cancellation.
		// --------------------------------------------------------

		if processCtx.Err() == context.DeadlineExceeded {

			fmt.Printf(
				"[WORKER %d] notification %d TIMEOUT\n",
				workerID,
				notification.ID,
			)

			return Result{
				NotificationID: notification.ID,
				Status:         "timeout",
				WorkerID:       workerID,
			}
		}

		fmt.Printf(
			"[WORKER %d] notification %d CANCELLED\n",
			workerID,
			notification.ID,
		)

		return Result{
			NotificationID: notification.ID,
			Status:         "cancelled",
			WorkerID:       workerID,
		}
	}
}

// ============================================================
// RESULT CONSUMER
// ============================================================

func (s *Service) ConsumeResults(ctx context.Context) {

	for {

		// --------------------------------------------------------
		// SELECT
		//
		// Consumer aynı anda:
		//
		// 1. Result bekliyor
		// 2. Cancellation bekliyor
		// --------------------------------------------------------

		select {

		case result := <-s.results:

			fmt.Printf(
				"[RESULT] notification=%d worker=%d status=%s\n",
				result.NotificationID,
				result.WorkerID,
				result.Status,
			)

		case <-ctx.Done():

			fmt.Println("[RESULT] consumer cancelled")
			return
		}
	}
}

// ============================================================
// MAIN
// ============================================================

func main() {

	rand.Seed(time.Now().UnixNano())

	// ========================================================
	// CONTEXT
	// ========================================================

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	// Program sonunda cancellation sinyali göndereceğiz.
	defer cancel()

	// ========================================================
	// CHANNELS
	// ========================================================

	// Küçük buffer özellikle backpressure'ı
	// görebilmemiz için kullanılıyor.
	notifications := make(chan Notification, 3)

	results := make(chan Result, 10)

	service := &Service{
		notifications: notifications,
		results:       results,
	}

	// ========================================================
	// WORKERS
	// ========================================================

	workerCount := 3

	for i := 1; i <= workerCount; i++ {

		service.wg.Add(1)

		go service.Worker(
			ctx,
			i,
		)
	}

	// ========================================================
	// RESULT CONSUMER
	// ========================================================

	go service.ConsumeResults(ctx)

	// ========================================================
	// PRODUCER
	// ========================================================

	go service.Produce(
		ctx,
		20,
	)

	// ========================================================
	// SYSTEM ÇALIŞIYOR
	// ========================================================

	fmt.Println()
	fmt.Println("================================")
	fmt.Println("Notification service started")
	fmt.Println("================================")
	fmt.Println()

	// Sistemi bir süre çalıştırıyoruz.
	time.Sleep(5 * time.Second)

	// ========================================================
	// GRACEFUL SHUTDOWN
	// ========================================================

	fmt.Println()
	fmt.Println("================================")
	fmt.Println("Shutdown requested")
	fmt.Println("================================")
	fmt.Println()

	// Bütün goroutine'lere:
	//
	// "Artık işinizi bırakıp kapanabilirsiniz."
	//
	// sinyali gönderiliyor.

	cancel()

	// Worker'ların kapanmasını bekliyoruz.
	service.wg.Wait()

	fmt.Println()
	fmt.Println("================================")
	fmt.Println("All workers stopped")
	fmt.Println("================================")
}
