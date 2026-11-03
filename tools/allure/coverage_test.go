package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two paths these tests hold are the ones the rest of the suite cannot reach
// from a valid run: a stream the reader fails on, and a verdict for a test that was
// never announced as started. The rest of the program is covered where it is used.
//
// main() is deliberately not covered. It is one statement — os.Exit of the status
// cli returns — and reaching it would end the test process. Everything that decides
// anything is in cli, which takes its streams as arguments and is asserted directly.

// covErrorReader is a stream that fails on its first read, which is what a broken
// pipe or an unreadable input looks like to the collector.
type covErrorReader struct{}

func (covErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestCovRunReportsAStreamItCannotRead holds the run to failing rather than
// reporting nothing: a truncated event stream is not an empty test run, and a
// report written from one would claim that no test failed.
func TestCovRunReportsAStreamItCannotRead(t *testing.T) {
	results, err := run(options{in: covErrorReader{}, out: t.TempDir(), warnings: io.Discard})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read test events")
	assert.Nil(t, results, "a run that could not read its stream reports no results")
}

// TestCovAVerdictWithoutAStartIsStillOneResult covers the pass, skip and fail events
// that arrive without the run event before them.
//
// Go emits a run event for every test it starts, so this is defensive: a stream
// assembled by hand, or truncated at the front by a rotated log, still ends with a
// verdict, and dropping it would lose the one fact the report exists to state. The
// result is built from the verdict's own package, name and timestamp instead of
// being discarded.
func TestCovAVerdictWithoutAStartIsStillOneResult(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, "pass", "TestOrphan", 0.5, ""),
		eventLine(t, "skip", "TestSkippedOrphan", 0.5, ""),
		eventLine(t, "fail", "TestFailedOrphan", 0.5, "--- FAIL: TestFailedOrphan\n"),
	))
	require.Len(t, results, 3, "every verdict is one result, announced or not")

	byTest := make(map[string]*result, len(results))
	for _, one := range results {
		byTest[one.test] = one
	}
	assert.Equal(t, statusPassed, byTest["TestOrphan"].status)
	assert.Equal(t, statusSkipped, byTest["TestSkippedOrphan"].status)
	assert.Equal(t, statusFailed, byTest["TestFailedOrphan"].status)
	// The stop time is the verdict's, because that is the only time the stream
	// stated for it; a result with no clock would be written as "unknown".
	assert.False(t, byTest["TestOrphan"].stop.IsZero(), "the verdict's own time is the result's stop")
}
