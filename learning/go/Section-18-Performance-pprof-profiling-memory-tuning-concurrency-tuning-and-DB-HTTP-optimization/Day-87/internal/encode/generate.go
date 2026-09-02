package encode

import (
	"fmt"
	"math/rand"
	"time"
)

// Sample builds a deterministic batch of records so every benchmark and
// profile sees exactly the same work.
func Sample(count int, seed int64) []Record {
	source := rand.New(rand.NewSource(seed))

	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	services := []string{"orders", "payments", "shipping", "catalog"}
	keys := []string{"order_id", "user", "route", "latency_ms", "attempt"}

	base := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)

	records := make([]Record, count)

	for i := range records {
		fieldCount := source.Intn(4)

		fields := make([]Field, fieldCount)

		for j := range fields {
			fields[j] = Field{
				Key:   keys[source.Intn(len(keys))],
				Value: fmt.Sprintf("%d", source.Intn(100000)),
			}
		}

		records[i] = Record{
			Time:    base.Add(time.Duration(i) * time.Second),
			Level:   levels[source.Intn(len(levels))],
			Service: services[source.Intn(len(services))],
			Message: fmt.Sprintf("handled request %d", i),
			Fields:  fields,
		}
	}

	return records
}
