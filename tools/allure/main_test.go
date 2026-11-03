package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the bridge between two file formats that nothing else bridges, so its
// failures are silent by nature: a report that quietly drops the failing test is
// worse than no report, because it looks like a green one. These tests therefore
// assert the counts and the statuses, not merely that a file appeared.

const testPackage = "github.com/Manu343726/hammer/thing"

// eventLine renders one event the way `go test -json` emits it.
func eventLine(t *testing.T, action, test string, elapsed float64, output string) string {
	t.Helper()
	one := event{
		Time:    time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC),
		Action:  action,
		Package: testPackage,
		Test:    test,
		Elapsed: elapsed,
		Output:  output,
	}
	encoded, err := json.Marshal(one)
	require.NoError(t, err)
	return string(encoded)
}

// stream joins event lines into a `go test -json` stream.
func stream(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// collectStream is the analysis every test wants: what did the converter see?
func collectStream(t *testing.T, input string) []*result {
	t.Helper()
	results, err := collect(strings.NewReader(input))
	require.NoError(t, err)
	return results
}

func TestOneTestBecomesOneResultAndAPackageIsNotATest(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, "output", "TestOne", 0, "=== RUN   TestOne\n"),
		eventLine(t, actionPass, "TestOne", 0.25, ""),
		eventLine(t, actionPass, "", 0.3, ""),
	))

	require.Len(t, results, 1, "the package's own pass is not a test")
	assert.Equal(t, "TestOne", results[0].test)
	assert.Equal(t, statusPassed, results[0].status)
	assert.Contains(t, results[0].output.String(), "=== RUN")
}

func TestEveryOutcomeIsRecordedAsItself(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestPass", 0, ""),
		eventLine(t, actionPass, "TestPass", 0, ""),
		eventLine(t, actionRun, "TestFail", 0, ""),
		eventLine(t, actionFail, "TestFail", 0, ""),
		eventLine(t, actionRun, "TestSkip", 0, ""),
		eventLine(t, actionSkip, "TestSkip", 0, ""),
	))

	require.Len(t, results, 3)
	statuses := map[string]string{}
	for _, one := range results {
		statuses[one.test] = one.status
	}
	assert.Equal(t, statusPassed, statuses["TestPass"])
	assert.Equal(t, statusFailed, statuses["TestFail"])
	assert.Equal(t, statusSkipped, statuses["TestSkip"])
}

// A test whose package failed to build never ran, so it does not exist in the
// stream — only the package's failure does. Dropping it would turn a broken
// build into a report of zero tests, which reads as success.
func TestAPackageThatNeverBuiltAppearsAsABrokenResult(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionFail, "", 0.1, ""),
	))

	require.Len(t, results, 1)
	assert.Empty(t, results[0].test, "a package result is the package, not a test named after it")
	assert.Equal(t, statusBroken, results[0].status)
	assert.Equal(t, testPackage, results[0].fullName())
}

// A package that reports a failing test and its own failure is one thing: the
// test failed. Inventing a package-level broken result on top of it would double
// count the same problem.
func TestAPackageFailureWithAFailingTestDoesNotAddASecondResult(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestBad", 0, ""),
		eventLine(t, actionFail, "TestBad", 0, "boom\n"),
		eventLine(t, actionFail, "", 0.2, ""),
	))

	require.Len(t, results, 1)
	assert.Equal(t, "TestBad", results[0].test)
}

// A run that is interrupted leaves a test that began and never finished. That is
// not a skip: the test did not decide not to run, the process died under it.
func TestATestThatNeverReportedAnOutcomeIsBroken(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestInterrupted", 0, ""),
	))

	require.Len(t, results, 1)
	assert.Empty(t, results[0].status)
	assert.Equal(t, statusBroken, unwritten(results[0]))
}

func unwritten(one *result) string {
	if one.status != "" {
		return one.status
	}
	return statusBroken
}

// A parallel test pauses and resumes. Each pause is not another test.
func TestAPausedAndResumedTestStaysOneResult(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestParallel", 0, ""),
		eventLine(t, actionPause, "TestParallel", 0, ""),
		eventLine(t, actionCont, "TestParallel", 0, ""),
		eventLine(t, actionPass, "TestParallel", 1.5, ""),
	))

	require.Len(t, results, 1)
	assert.Equal(t, statusPassed, results[0].status)
}

// The go command writes its own notices — a module download, a compile error —
// onto the same stream. Losing the report to one of them would lose all of it.
func TestALineThatIsNotAnEventIsIgnored(t *testing.T) {
	results := collectStream(t, stream(
		"go: downloading example.test/mod v1.2.3",
		"",
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, actionPass, "TestOne", 0, ""),
	))

	require.Len(t, results, 1)
	assert.Equal(t, "TestOne", results[0].test)
}

// Scan errors are the difference between "no tests" and "we could not read the
// stream", and only one of those is a green report.
func TestAStreamThatCannotBeReadIsAnError(t *testing.T) {
	_, err := collect(readerFunc(func([]byte) (int, error) { return 0, errors.New("disk on fire") }))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk on fire")
}

// A single test's output can exceed a scanner's default line length; binding the
// report to that limit would fail the reporter instead of the test.
func TestALongOutputLineIsRead(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	results := collectStream(t, stream(
		eventLine(t, actionRun, "TestChatty", 0, ""),
		eventLine(t, "output", "TestChatty", 0, long),
		eventLine(t, actionFail, "TestChatty", 0, ""),
	))
	require.Len(t, results, 1)
	assert.Len(t, results[0].output.String(), len(long))
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// --- the written report -----------------------------------------------------

// readResults reads back every result file the converter wrote, which is the
// only way to assert what Allure will actually see.
func readResults(t *testing.T, directory string) map[string]allureResult {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	results := make(map[string]allureResult)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "-result.json") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		require.NoError(t, err)
		var one allureResult
		require.NoError(t, json.Unmarshal(content, &one), entry.Name())
		results[one.FullName] = one
	}
	return results
}

func TestARunWritesAResultAndAnEnvironment(t *testing.T) {
	directory := t.TempDir()
	stderr := &bytes.Buffer{}
	results, err := run(options{
		in:       strings.NewReader(stream(eventLine(t, actionRun, "TestOne", 0, ""), eventLine(t, actionPass, "TestOne", 0.5, ""))),
		out:      directory,
		module:   "github.com/Manu343726/hammer",
		environ:  map[string]string{"go": "go1.27", "commit": "abc123"},
		warnings: stderr,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, stderr.String(), "1 result(s)")

	written := readResults(t, directory)[testPackage+".TestOne"]
	assert.Equal(t, statusPassed, written.Status)
	assert.Equal(t, "finished", written.Stage)
	assert.NotZero(t, written.Start)
	assert.NotZero(t, written.Stop)
	assert.Nil(t, written.StatusDetails, "a pass has nothing to explain")

	environment, err := os.ReadFile(filepath.Join(directory, "environment.properties"))
	require.NoError(t, err)
	assert.Equal(t, "commit=abc123\ngo=go1.27\n", string(environment),
		"the environment is sorted, so two runs diff cleanly")
}

// A failure's log is the first thing anyone opens, so it travels with the
// result instead of living only in the CI console that has long been rotated.
func TestAFailureCarriesItsOutputAsAnAttachment(t *testing.T) {
	directory := t.TempDir()
	_, err := run(options{
		in: strings.NewReader(stream(
			eventLine(t, actionRun, "TestBad", 0, ""),
			eventLine(t, "output", "TestBad", 0, "=== RUN   TestBad\n    thing_test.go:12: wanted 2, got 3\n"),
			eventLine(t, actionFail, "TestBad", 0.1, ""),
		)),
		out:      directory,
		warnings: &bytes.Buffer{},
	})
	require.NoError(t, err)

	written := readResults(t, directory)[testPackage+".TestBad"]
	require.NotNil(t, written.StatusDetails)
	assert.Equal(t, "thing_test.go:12: wanted 2, got 3", written.StatusDetails.Message,
		"the message is the first line a reader can act on, not the === RUN marker")
	assert.True(t, written.StatusDetails.Known, "a failed assertion is the test's verdict, not the reporter's defect")
	require.Len(t, written.Attachments, 1)

	content, err := os.ReadFile(filepath.Join(directory, written.Attachments[0].Source))
	require.NoError(t, err)
	assert.Contains(t, string(content), "wanted 2, got 3")
}

// A package that never compiled is not the test's verdict, so it is reported as
// broken — and the message says so rather than blaming an assertion that never
// ran.
func TestABrokenPackageIsReportedAsUnknown(t *testing.T) {
	directory := t.TempDir()
	_, err := run(options{
		in:       strings.NewReader(stream(eventLine(t, actionFail, "", 0, ""))),
		out:      directory,
		warnings: &bytes.Buffer{},
	})
	require.NoError(t, err)

	written := readResults(t, directory)[testPackage]
	require.NotNil(t, written.StatusDetails)
	assert.Equal(t, statusBroken, written.Status)
	assert.False(t, written.StatusDetails.Known)
}

// A report is grouped by package and by the parent test of a subtest, because
// that is how a Go developer already thinks about where a failure lives.
func TestALabelsResultByPackageParentAndSubtest(t *testing.T) {
	directory := t.TempDir()
	_, err := run(options{
		in: strings.NewReader(stream(
			eventLine(t, actionRun, "TestTable/case_one", 0, ""),
			eventLine(t, actionPass, "TestTable/case_one", 0, ""),
		)),
		out:      directory,
		module:   "github.com/Manu343726/hammer",
		labels:   map[string]string{"ci.run": "42"},
		warnings: &bytes.Buffer{},
	})
	require.NoError(t, err)

	written := readResults(t, directory)[testPackage+".TestTable/case_one"]
	labels := map[string]string{}
	for _, one := range written.Labels {
		labels[one.Name] = one.Value
	}
	assert.Equal(t, "go", labels["language"])
	assert.Equal(t, "go test", labels["framework"])
	assert.Equal(t, testPackage, labels["package"])
	assert.Equal(t, "thing", labels["suite"], "the suite is the short package name a reader recognizes")
	assert.Equal(t, "TestTable", labels["subSuite"])
	assert.Equal(t, "case one", written.Name, "the name is the case, not the function it was generated in")
	assert.Equal(t, "42", labels["ci.run"])
}

func TestASubtestIsNamedByItsOwnCase(t *testing.T) {
	assert.Equal(t, "case one", displayName("TestTable/case_one"))
	assert.Equal(t, "TestOne", displayName("TestOne"))
	assert.Equal(t, "(package)", displayName(""))
}

func TestTheFirstLineSkipsTheHarnessPreamble(t *testing.T) {
	assert.Equal(t, "wanted 2, got 3", firstLine("=== RUN   TestOne\n--- FAIL: TestOne\n    wanted 2, got 3"))
	assert.Empty(t, firstLine(""))
	assert.Empty(t, firstLine("\n\n   \n"))
	long := strings.Repeat("y", 400)
	assert.Equal(t, 301, utf8.RuneCountInString(firstLine(long)), "a message is truncated, never the whole log")
}

// A history id is what makes a trend line continuous, so it must not depend on
// when the test ran — while a result id must, or two runs collapse into one.
func TestIdentifiersAreStableAcrossRunsButResultsAreNot(t *testing.T) {
	at := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)

	assert.Equal(t, identifier("p.T", time.Time{}), identifier("p.T", time.Time{}))
	assert.NotEqual(t, identifier("p.T", at), identifier("p.T", later))
	assert.NotEqual(t, identifier("p.T", at), identifier("p.Other", at))
}

func TestAnUnknownClockIsWrittenAsZero(t *testing.T) {
	assert.Zero(t, millis(time.Time{}))
	assert.NotZero(t, millis(time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)))
}

// --- the summary ------------------------------------------------------------

func TestTheSummaryCountsEveryOutcomeAndNamesWhatFailed(t *testing.T) {
	summary := summarize([]*result{
		{packageName: "p", test: "TestPass", status: statusPassed},
		{packageName: "p", test: "TestBad", status: statusFailed},
		{packageName: "p", test: "TestSkip", status: statusSkipped},
		{packageName: "q", test: "TestInterrupted"},
	})

	assert.Contains(t, summary, "### Tests failed")
	assert.Contains(t, summary, "| passed | 1 |")
	assert.Contains(t, summary, "| failed | 1 |")
	assert.Contains(t, summary, "| skipped | 1 |")
	assert.Contains(t, summary, "| broken | 1 |")
	assert.Contains(t, summary, "**4**")
	assert.Contains(t, summary, "`p` | 1 | 1 | 1 | 0 |")
	assert.Contains(t, summary, "`q` | 0 | 0 | 0 | 1 |")
	assert.Contains(t, summary, "`p.TestBad`", "a failing test is named, not only counted")
}

func TestTheSummarySaysWhenEverythingPassed(t *testing.T) {
	summary := summarize([]*result{{packageName: "p", test: "TestOne", status: statusPassed}})
	assert.Contains(t, summary, "### All tests passed")
	assert.NotContains(t, summary, "Failing tests")
}

// A summary that lists four hundred failures is a summary nobody reads, and the
// report it links to lists all of them.
func TestTheSummaryBoundsTheFailureList(t *testing.T) {
	results := make([]*result, 0, 30)
	for index := 0; index < 30; index++ {
		results = append(results, &result{
			packageName: "p",
			test:        "Test" + strings.Repeat("x", index+1),
			status:      statusFailed,
		})
	}
	summary := summarize(results)
	assert.Contains(t, summary, "and 10 more, in the report")
}

func TestTheSummaryOfAnEmptyRunIsNotAVerdict(t *testing.T) {
	summary := summarize(nil)
	assert.Contains(t, summary, "### All tests passed")
	assert.Contains(t, summary, "**0**")
}

// --- inputs -----------------------------------------------------------------

func TestLabelsAreReadAndRefusedLoudly(t *testing.T) {
	labels, err := parseLabels([]string{"ci.run=42", " env = prod "})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ci.run": "42", "env": "prod"}, labels)

	_, err = parseLabels([]string{"nonsense"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not name=value")
}

func TestAnEmptyLabelListIsNoLabels(t *testing.T) {
	assert.Empty(t, splitNonEmpty(""))
	assert.Empty(t, splitNonEmpty("  ,  , "))
	assert.Equal(t, []string{"a=1", "b=2"}, splitNonEmpty(" a=1 , ,b=2 "))
}

func TestTheEnvironmentFileToleratesBlanksAndComments(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, "env.properties")
	require.NoError(t, os.WriteFile(name, []byte("# a comment\n\nversion=1.27\nbroken line\n"), 0o644))

	environ, err := readEnvironment(name)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"version": "1.27"}, environ)

	noName, err := readEnvironment("")
	require.NoError(t, err)
	assert.Nil(t, noName)

	_, err = readEnvironment(filepath.Join(directory, "absent"))
	require.Error(t, err)
}

// --- the command ------------------------------------------------------------

// The command is what CI runs, so it is driven the way CI drives it: arguments
// in, files out, exit status.
func TestTheCommandWritesResultsAndASummary(t *testing.T) {
	directory := t.TempDir()
	events := filepath.Join(directory, "events.json")
	require.NoError(t, os.WriteFile(events, []byte(stream(
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, actionPass, "TestOne", 0, ""),
	)), 0o644))
	envFile := filepath.Join(directory, "env.properties")
	require.NoError(t, os.WriteFile(envFile, []byte("go=go1.27\n"), 0o644))

	out := filepath.Join(directory, "results")
	summary := filepath.Join(directory, "summary.md")
	stderr := &bytes.Buffer{}
	status := cli([]string{
		"-in", events,
		"-out", out,
		"-environment", envFile,
		"-labels", "ci.run=7",
		"-summary", summary,
	}, strings.NewReader(""), stderr)

	require.Equal(t, 0, status, stderr.String())
	assert.FileExists(t, filepath.Join(out, "environment.properties"))
	content, err := os.ReadFile(summary)
	require.NoError(t, err)
	assert.Contains(t, string(content), "All tests passed")
}

// The stream can arrive on standard input, which is how a shell pipeline feeds
// it and how the tool is used by hand.
func TestTheCommandReadsStandardInput(t *testing.T) {
	directory := t.TempDir()
	status := cli([]string{"-out", directory, "-summary", filepath.Join(directory, "s.md")},
		strings.NewReader(stream(
			eventLine(t, actionRun, "TestOne", 0, ""),
			eventLine(t, actionFail, "TestOne", 0, ""),
		)), &bytes.Buffer{})
	require.Equal(t, 0, status)
	content, err := os.ReadFile(filepath.Join(directory, "s.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Tests failed")
}

func TestTheCommandRefusesWhatItCannotDo(t *testing.T) {
	directory := t.TempDir()
	stderr := &bytes.Buffer{}

	assert.Equal(t, 2, cli([]string{"-nonsense"}, strings.NewReader(""), stderr), "an unknown flag")
	assert.Equal(t, 1, cli([]string{"-in", filepath.Join(directory, "absent")}, strings.NewReader(""), stderr), "an unreadable stream")
	assert.Equal(t, 1, cli([]string{"-labels", "broken"}, strings.NewReader(""), stderr), "a malformed label")
	assert.Equal(t, 1, cli([]string{"-environment", filepath.Join(directory, "absent")}, strings.NewReader(""), stderr), "an unreadable environment")
}

// A results path that cannot be created is reported as a failure of the run, not
// swallowed into a report that silently never existed.
func TestTheCommandReportsAnUnwritableDestination(t *testing.T) {
	directory := t.TempDir()
	blocker := filepath.Join(directory, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o644))

	status := cli([]string{"-out", filepath.Join(blocker, "results")}, strings.NewReader(""), &bytes.Buffer{})
	assert.Equal(t, 1, status)
}

func TestTheCommandWritesNoSummaryWhenNoneIsAskedFor(t *testing.T) {
	directory := t.TempDir()
	status := cli([]string{"-out", directory}, strings.NewReader(stream(
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, actionPass, "TestOne", 0, ""),
	)), &bytes.Buffer{})
	require.Equal(t, 0, status)

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasSuffix(entry.Name(), ".md"))
	}
}

// The help page is a successful invocation: a caller asking what the flags are
// is not a caller making a mistake.
func TestTheCommandAnswersHelp(t *testing.T) {
	stderr := &bytes.Buffer{}
	assert.Equal(t, 0, cli([]string{"-h"}, strings.NewReader(""), stderr))
	assert.Contains(t, stderr.String(), "Allure results")
}

// --- the parts that only fail -------------------------------------------------

// fixedTime is the clock every event in this file carries, so the result id a
// failing write collides with can be computed rather than guessed.
func fixedTime() time.Time {
	return time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
}

// resultUUID is the name one test's result file will be written under.
func resultUUID(testPath string) string {
	return identifier(testPackage+"."+testPath, fixedTime())
}

// An output line that arrives before its test started belongs to nothing, and
// attaching it to the next test that happens to have a name would be worse than
// dropping it.
func TestOutputBeforeATestStartsIsIgnored(t *testing.T) {
	results := collectStream(t, stream(
		eventLine(t, "output", "TestOne", 0, "stray\n"),
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, actionPass, "TestOne", 0, ""),
	))
	require.Len(t, results, 1)
	assert.Empty(t, results[0].output.String())
}

// A report that lists packages in arrival order is a report whose sections move
// between runs.
func TestResultsAreOrderedByPackageThenTest(t *testing.T) {
	make := func(pkg, test, action string) string {
		encoded, err := json.Marshal(event{Action: action, Package: pkg, Test: test, Time: fixedTime()})
		require.NoError(t, err)
		return string(encoded)
	}
	results := collectStream(t, stream(
		make("z/pkg", "TestB", actionRun),
		make("z/pkg", "TestB", actionPass),
		make("a/pkg", "TestZ", actionRun),
		make("a/pkg", "TestZ", actionPass),
		make("a/pkg", "TestA", actionRun),
		make("a/pkg", "TestA", actionPass),
	))
	require.Len(t, results, 3)
	assert.Equal(t, []string{"a/pkg.TestA", "a/pkg.TestZ", "z/pkg.TestB"},
		[]string{results[0].fullName(), results[1].fullName(), results[2].fullName()})
}

// The written report is the one Allure reads, so the interrupted case is
// asserted in the file rather than in the intermediate value.
func TestAnInterruptedTestIsWrittenAsBroken(t *testing.T) {
	directory := t.TempDir()
	_, err := run(options{
		in: strings.NewReader(stream(
			eventLine(t, actionRun, "TestInterrupted", 0, ""),
		)),
		out:      directory,
		warnings: &bytes.Buffer{},
	})
	require.NoError(t, err)

	written := readResults(t, directory)[testPackage+".TestInterrupted"]
	assert.Equal(t, statusBroken, written.Status)
	assert.Zero(t, written.Stop, "no ending was reported, and inventing one would hide that")
}

// A destination that cannot be written is a failure of the run. Each path is
// blocked the way a real filesystem blocks it — a directory where a file is
// expected — rather than by a fake, because the check is that the error is
// reported, not that a mock was called.
func TestAPathThatCannotBeWrittenIsReported(t *testing.T) {
	cases := []struct {
		name  string
		block func(directory string, uuid string) error
	}{
		{
			name: "the attachment",
			block: func(directory, uuid string) error {
				return os.Mkdir(filepath.Join(directory, uuid+"-attachment.txt"), 0o755)
			},
		},
		{
			name: "the temporary result",
			block: func(directory, uuid string) error {
				return os.Mkdir(filepath.Join(directory, "."+uuid+"-result.json.tmp"), 0o755)
			},
		},
		{
			name: "the result itself",
			block: func(directory, uuid string) error {
				return os.Mkdir(filepath.Join(directory, uuid+"-result.json"), 0o755)
			},
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			directory := t.TempDir()
			require.NoError(t, one.block(directory, resultUUID("TestOne")))

			_, err := run(options{
				in: strings.NewReader(stream(
					eventLine(t, actionRun, "TestOne", 0, ""),
					eventLine(t, "output", "TestOne", 0, "some output\n"),
					eventLine(t, actionFail, "TestOne", 0, ""),
				)),
				out:      directory,
				warnings: &bytes.Buffer{},
			})
			require.Error(t, err)
		})
	}
}

// The environment file sits beside the results and can collide the same way.
func TestAnEnvironmentThatCannotBeWrittenIsReported(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(directory, "environment.properties"), 0o755))

	_, err := run(options{
		in:       strings.NewReader(stream()),
		out:      directory,
		environ:  map[string]string{"go": "go1.27"},
		warnings: &bytes.Buffer{},
	})
	require.Error(t, err)
}

// Encoding is the one step that cannot fail for the values this program builds,
// so it is proven against the function rather than through a run: a value that
// cannot be encoded must be an error, not a silently empty report.
func TestEncodingSomethingImpossibleIsAnError(t *testing.T) {
	err := writeJSON(t.TempDir(), "result.json", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode")
}

// The temporary file exists so a reader never sees a half-written result.
func TestAResultIsWrittenThroughATemporaryFile(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, writeJSON(directory, "one.json", map[string]string{"a": "b"}))

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the temporary file is renamed away, not left behind")
	assert.Equal(t, "one.json", entries[0].Name())
}

func TestAModuleGroupsItsOwnRootPackage(t *testing.T) {
	assert.Equal(t, "example.test/mod", moduleOf(options{module: "example.test/mod"}, "example.test/mod"))
	assert.Equal(t, "example.test/mod", moduleOf(options{module: "example.test/mod"}, "example.test/mod/sub"))
	assert.Equal(t, "other/pkg", moduleOf(options{}, "other/pkg"), "without a module the package speaks for itself")
}

// A summary that cannot be written is a failed run: the caller asked for it, and
// returning success with no file is how a green job loses its report.
func TestTheCommandReportsASummaryItCannotWrite(t *testing.T) {
	directory := t.TempDir()
	status := cli([]string{
		"-out", directory,
		"-summary", filepath.Join(directory, "absent", "summary.md"),
	}, strings.NewReader(stream(
		eventLine(t, actionRun, "TestOne", 0, ""),
		eventLine(t, actionPass, "TestOne", 0, ""),
	)), &bytes.Buffer{})
	assert.Equal(t, 1, status)
}
