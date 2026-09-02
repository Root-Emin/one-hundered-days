// Package encode renders log records as text lines. It is the same output four
// ways, from the version that allocates constantly to the version that
// allocates nothing per call.
//
// The progression is the lesson, and it is the order you should apply it in:
//
//	EncodeNaive     fmt.Sprintf + string concat            baseline
//	EncodePrealloc  make([]byte, 0, estimate) + strconv    task 2 and 3
//	AppendEncode    caller-provided buffer                 the idiomatic API
//	Encoder.Encode  sync.Pool behind the same API          task 1, last resort
//
// Preallocation and strings.Builder are cheap, local and always safe. A pool
// is none of those things, so it comes last and only with a benchmark behind
// it.
package encode

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87/internal/bufpool"
)

type Field struct {
	Key   string
	Value string
}

type Record struct {
	Time    time.Time
	Level   string
	Service string
	Message string
	Fields  []Field
}

// Line format: 2026-09-02T01:00:00Z INFO  orders  "message" key=value key=value
const timeLayout = "2006-01-02T15:04:05Z"

//
// 1. THE BASELINE
//

// EncodeNaive is what the first draft looks like: correct, readable, and
// allocating several times per record.
//
// Every fmt.Sprintf allocates a result string and boxes each argument into an
// interface. Every += allocates a new string and copies everything so far.
func EncodeNaive(records []Record) []byte {
	output := ""

	for _, record := range records {
		line := fmt.Sprintf("%s %-5s %s %q",
			record.Time.UTC().Format(timeLayout), record.Level, record.Service, record.Message)

		for _, field := range record.Fields {
			line += fmt.Sprintf(" %s=%s", field.Key, field.Value)
		}

		output += line + "\n"
	}

	return []byte(output)
}

//
// 2. PREALLOCATION AND A BUILDER
//

// EncodePrealloc sizes the buffer once and appends into it.
//
// The two changes that matter:
//
//   - one allocation of a known size, instead of a new string per operation.
//     append() grows by doubling, so an unsized buffer for n bytes allocates
//     log2(n) times and copies everything each time.
//   - strconv.Append* and time.AppendFormat write INTO the buffer instead of
//     returning a new string for us to copy.
func EncodePrealloc(records []Record) []byte {
	buffer := make([]byte, 0, EstimateSize(records))

	for i := range records {
		buffer = AppendRecord(buffer, &records[i])
	}

	return buffer
}

// EstimateSize is a rough upper bound. Being roughly right removes nearly all
// the growth reallocations; being exact is not worth the arithmetic.
func EstimateSize(records []Record) int {
	size := 0

	for i := range records {
		record := &records[i]

		size += len(timeLayout) + 1 + 6 + len(record.Service) + len(record.Message) + 4

		for _, field := range record.Fields {
			size += len(field.Key) + len(field.Value) + 2
		}

		size++
	}

	return size
}

// AppendRecord follows Go's append convention: take a destination, return the
// possibly-reallocated result. It is how the standard library lets a caller
// decide where the memory comes from (see time.AppendFormat, strconv.AppendInt).
func AppendRecord(dst []byte, record *Record) []byte {
	dst = record.Time.UTC().AppendFormat(dst, timeLayout)
	dst = append(dst, ' ')

	dst = append(dst, record.Level...)

	// Pad to 5 columns without fmt's %-5s.
	for i := len(record.Level); i < 5; i++ {
		dst = append(dst, ' ')
	}

	dst = append(dst, ' ')
	dst = append(dst, record.Service...)
	dst = append(dst, ' ')

	// strconv.AppendQuote does the same escaping as %q, into our buffer.
	dst = strconv.AppendQuote(dst, record.Message)

	for _, field := range record.Fields {
		dst = append(dst, ' ')
		dst = append(dst, field.Key...)
		dst = append(dst, '=')
		dst = append(dst, field.Value...)
	}

	return append(dst, '\n')
}

// AppendEncode is the API shape to prefer for a hot path: the caller supplies
// the buffer, so the caller decides whether to reuse it. No pool, no global
// state, and zero allocations when the buffer is already big enough.
func AppendEncode(dst []byte, records []Record) []byte {
	for i := range records {
		dst = AppendRecord(dst, &records[i])
	}

	return dst
}

// BuildString is the strings.Builder version, for when the result must be a
// string. Builder's whole purpose is to hand its buffer to the string without
// the final copy that string(buffer) costs.
func BuildString(records []Record) string {
	var builder strings.Builder

	builder.Grow(EstimateSize(records))

	scratch := make([]byte, 0, 256)

	for i := range records {
		scratch = AppendRecord(scratch[:0], &records[i])

		builder.Write(scratch)
	}

	return builder.String()
}

//
// 3. THE POOL
//

// Encoder reuses buffers through a sync.Pool.
//
// This is the version to reach for LAST: when a profile shows a hot path
// allocating large short-lived buffers, per request, under concurrency. For
// anything smaller, AppendEncode with a caller-owned buffer is simpler and
// usually just as fast.
type Encoder struct {
	pool *bufpool.Pool
}

func NewEncoder() *Encoder {
	return &Encoder{pool: bufpool.New()}
}

func (e *Encoder) Stats() (gets, allocations, discards int64) {
	return e.pool.Stats()
}

// WriteTo encodes straight into w, holding only one pooled buffer.
//
// Note the shape: the buffer never escapes this function. Returning it -
// or the result of string(buffer) built on top of it - would mean the caller
// could hold a reference to memory the pool has already handed to someone
// else, which is the classic sync.Pool bug.
func (e *Encoder) WriteTo(w io.Writer, records []Record) (int, error) {
	buffer := e.pool.Get()
	defer e.pool.Put(buffer)

	*buffer = AppendEncode(*buffer, records)

	written, err := w.Write(*buffer)
	if err != nil {
		return written, fmt.Errorf("write records: %w", err)
	}

	return written, nil
}

// Encode returns a copy, because the pooled buffer goes back immediately.
//
// The copy is the honest price of a []byte-returning API on top of a pool.
// It is still one allocation instead of several dozen - but if you find
// yourself writing this, check whether WriteTo would do.
func (e *Encoder) Encode(records []Record) []byte {
	buffer := e.pool.Get()
	defer e.pool.Put(buffer)

	*buffer = AppendEncode(*buffer, records)

	result := make([]byte, len(*buffer))
	copy(result, *buffer)

	return result
}
