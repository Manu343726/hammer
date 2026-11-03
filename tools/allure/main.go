// Command allurereport converts a `go test -json` stream into the result files
// Allure 3 reads.
//
// It exists because the two halves of that sentence do not meet anywhere else:
// Go writes a stream of test events, Allure reads one JSON file per test, and
// the usual bridge is a chain of three tools and two file formats. This is one
// process, one format in, one format out, and no dependency added to the test
// suite itself — the tests stay plain `go test` and plain testify, which is what
// they are good at.
//
// Usage:
//
//	go test -json ./... > test.json
//	go run ./tools/allure -in test.json -out build/allure-results
//	allure generate build/allure-results -o allure-report
//
// The output is the Allure 3 result schema: one <uuid>-result.json per test, and
// an environment.properties describing the machine the run happened on. A test's
// own output is attached to its result rather than discarded, because the first
// thing anyone does with a red report is read the log it came from.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Test actions `go test -json` reports. Only the ones that change a result's
// state are named here; everything else is output.
const (
	actionRun   = "run"
	actionPass  = "pass"
	actionFail  = "fail"
	actionSkip  = "skip"
	actionPause = "pause"
	actionCont  = "cont"
)

// Allure statuses. A Go test has three outcomes, and they are not Allure's
// four: a failure is `failed` because the test ran and disagreed, and `broken`
// is reserved for the run that never produced a verdict at all.
const (
	statusPassed  = "passed"
	statusFailed  = "failed"
	statusBroken  = "broken"
	statusSkipped = "skipped"
)

// event is one line of the `go test -json` stream.
type event struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
}

// result is one test's worth of accumulated events, and what becomes one file.
type result struct {
	packageName string
	test        string
	start       time.Time
	stop        time.Time
	status      string
	output      strings.Builder
}

// fullName is the identifier a reader recognizes as one test across runs, and
// therefore the thing a history id is derived from.
func (r *result) fullName() string {
	if r.test == "" {
		return r.packageName
	}
	return r.packageName + "." + r.test
}

// allureResult is the shape Allure 3 reads. It is deliberately a struct rather
// than a map: a field this program forgets to set should be a visible zero, not
// a key that silently never appears.
type allureResult struct {
	UUID          string         `json:"uuid"`
	HistoryID     string         `json:"historyId"`
	Name          string         `json:"name"`
	FullName      string         `json:"fullName"`
	Status        string         `json:"status"`
	StatusDetails *statusDetails `json:"statusDetails,omitempty"`
	Stage         string         `json:"stage"`
	Start         int64          `json:"start"`
	Stop          int64          `json:"stop"`
	Labels        []label        `json:"labels"`
	Steps         []any          `json:"steps"`
	Attachments   []attachment   `json:"attachments,omitempty"`
}

type statusDetails struct {
	Known   bool   `json:"known"`
	Muted   bool   `json:"muted"`
	Flaky   bool   `json:"flaky"`
	Message string `json:"message,omitempty"`
	Trace   string `json:"trace,omitempty"`
}

type label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type attachment struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Type   string `json:"type"`
}

// options are what a run needs to know that the event stream does not say.
type options struct {
	in       io.Reader
	out      string
	labels   map[string]string
	module   string
	environ  map[string]string
	warnings io.Writer
}

// run reads every event and writes every result.
func run(options options) ([]*result, error) {
	results, err := collect(options.in)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(options.out, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", options.out, err)
	}
	for _, one := range results {
		if err := write(options, one); err != nil {
			return nil, err
		}
	}
	if err := writeEnvironment(options); err != nil {
		return nil, err
	}
	fmt.Fprintf(options.warnings, "%d result(s) written to %s\n", len(results), options.out)
	return results, nil
}

// summarize is the run's own account of itself, as markdown.
//
// It exists because the report is an HTML page somebody has to open, and the
// question "did this run pass, and what broke" is asked far more often than
// that. The output is a Markdown fragment, so whatever is collecting it — a
// GitHub Actions summary, a comment, a release note — only has to place it, not
// reformat it.
func summarize(results []*result) string {
	totals := map[string]int{statusPassed: 0, statusFailed: 0, statusSkipped: 0, statusBroken: 0}
	byPackage := make(map[string]map[string]int)
	failures := make([]*result, 0, 8)

	for _, one := range results {
		status := one.status
		if status == "" {
			status = statusBroken
		}
		totals[status]++
		if byPackage[one.packageName] == nil {
			byPackage[one.packageName] = make(map[string]int)
		}
		byPackage[one.packageName][status]++
		if status == statusFailed || status == statusBroken {
			failures = append(failures, one)
		}
	}

	text := &strings.Builder{}
	verdict := "All tests passed"
	if totals[statusFailed] > 0 || totals[statusBroken] > 0 {
		verdict = "Tests failed"
	}
	fmt.Fprintf(text, "### %s\n\n", verdict)
	fmt.Fprintf(text, "| Outcome | Tests |\n| --- | ---: |\n")
	for _, status := range []string{statusPassed, statusFailed, statusSkipped, statusBroken} {
		fmt.Fprintf(text, "| %s | %d |\n", status, totals[status])
	}
	fmt.Fprintf(text, "| **total** | **%d** |\n\n", len(results))

	packages := make([]string, 0, len(byPackage))
	for name := range byPackage {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	fmt.Fprintf(text, "| Package | Passed | Failed | Skipped | Broken |\n| --- | ---: | ---: | ---: | ---: |\n")
	for _, name := range packages {
		counts := byPackage[name]
		fmt.Fprintf(text, "| `%s` | %d | %d | %d | %d |\n",
			name, counts[statusPassed], counts[statusFailed], counts[statusSkipped], counts[statusBroken])
	}

	if len(failures) > 0 {
		// Bounded on purpose: a summary that lists four hundred failures is a
		// summary nobody reads, and the report it links to lists all of them.
		const limit = 20
		fmt.Fprintf(text, "\n#### Failing tests\n\n")
		for index, one := range failures {
			if index == limit {
				fmt.Fprintf(text, "- …and %d more, in the report\n", len(failures)-limit)
				break
			}
			fmt.Fprintf(text, "- `%s` — %s\n", one.fullName(), firstLine(one.output.String()))
		}
	}
	return text.String()
}

// collect folds the event stream into one result per test.
//
// The order the stream arrives in is the order tests ran, and a package that
// failed to build produces no test events at all — only its own failure. That
// case still has to appear in the report, or a broken build reads as "no tests
// ran, all good", so a package failure with no test behind it becomes a result
// of its own named after the package.
func collect(in io.Reader) ([]*result, error) {
	byName := make(map[string]*result)
	order := make([]string, 0, 64)
	// failedPackages records a package-level failure, which is what a build
	// error looks like. It is resolved at the end, once it is known whether any
	// test in that package reported a failure of its own.
	failedPackages := make(map[string]string)

	scanner := bufio.NewScanner(in)
	// A single test's output can exceed the default 64 KiB line, and a scanner
	// that stops at an arbitrary length turns a chatty test into a failed run of
	// this program rather than a failed test.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var one event
		if err := json.Unmarshal(line, &one); err != nil {
			// A line that is not an event is output from the go command itself
			// (a download notice, a compile error), and losing the rest of the
			// stream to it would lose the whole report.
			continue
		}
		key := one.Package + "\x00" + one.Test
		current, seen := byName[key]
		switch one.Action {
		case actionRun:
			current = &result{packageName: one.Package, test: one.Test, start: one.Time}
			byName[key] = current
			order = append(order, key)
			seen = true
		case actionPause, actionCont:
			// A parallel test pauses and resumes; it is still one test, and its
			// start is when it first ran.
		case actionPass, actionFail, actionSkip:
			if one.Test == "" {
				// A package's own verdict is not a test. A failure here with no
				// failing test behind it is a build that never ran, and is
				// turned into a result after the scan.
				if one.Action == actionFail {
					failedPackages[one.Package] = ""
				}
				continue
			}
			if !seen {
				current = &result{packageName: one.Package, test: one.Test, start: one.Time}
				byName[key] = current
				order = append(order, key)
				seen = true
			}
			switch one.Action {
			case actionPass:
				current.status = statusPassed
			case actionSkip:
				current.status = statusSkipped
			case actionFail:
				current.status = statusFailed
			}
			current.stop = one.Time
		case "output":
			if !seen {
				continue
			}
			current.output.WriteString(one.Output)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read test events: %w", err)
	}

	out := make([]*result, 0, len(order))
	failedAny := make(map[string]bool, len(failedPackages))
	for _, key := range order {
		one := byName[key]
		if one.test != "" && one.status == statusFailed {
			failedAny[one.packageName] = true
		}
		out = append(out, one)
	}
	for name := range failedPackages {
		if failedAny[name] {
			continue
		}
		// No test in this package reported a failure, so the failure is the
		// package's: a compile error, a panic in an init, a missing module. It
		// is broken rather than failed, and it needs a name a reader can act on.
		out = append(out, &result{
			packageName: name,
			status:      statusBroken,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].packageName != out[j].packageName {
			return out[i].packageName < out[j].packageName
		}
		return out[i].test < out[j].test
	})
	return out, nil
}

// write emits one test's result file and, when there is one, its attachment.
func write(options options, one *result) error {
	uuid := identifier(one.fullName(), one.start)
	attachmentList, err := writeAttachment(options, uuid, one)
	if err != nil {
		return err
	}

	status := one.status
	if status == "" {
		// A test that began and never reported an outcome is not a pass: the
		// run stopped underneath it. Reporting it as skipped would be a lie in
		// the one direction that matters.
		status = statusBroken
	}

	result := allureResult{
		UUID:      uuid,
		HistoryID: identifier(one.fullName(), time.Time{}),
		Name:      displayName(one.test),
		FullName:  one.fullName(),
		Status:    status,
		Stage:     "finished",
		Start:     millis(one.start),
		Stop:      millis(one.stop),
		Labels:    labels(options, one),
		Steps:     []any{},
	}
	result.Attachments = attachmentList
	if status != statusPassed {
		result.StatusDetails = &statusDetails{
			// Known is what tells Allure this status is understood rather than
			// a defect in the reporter; leaving it false makes every failure
			// read as an infrastructure problem.
			Known:   status != statusBroken,
			Message: firstLine(one.output.String()),
			Trace:   one.output.String(),
		}
	}
	return writeJSON(options.out, uuid+"-result.json", result)
}

// writeAttachment stores the test's output beside the result, if it produced
// any.
func writeAttachment(options options, uuid string, one *result) ([]attachment, error) {
	output := one.output.String()
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	name := uuid + "-attachment.txt"
	if err := os.WriteFile(filepath.Join(options.out, name), []byte(output), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", name, err)
	}
	return []attachment{{
		Name:   "test output",
		Source: name,
		Type:   "text/plain",
	}}, nil
}

// labels are what Allure groups a report by. A package is the suite, because
// that is the unit a Go developer already thinks in.
func labels(options options, one *result) []label {
	short := one.packageName
	if index := strings.LastIndex(short, "/"); index >= 0 {
		short = short[index+1:]
	}
	labels := []label{
		{Name: "language", Value: "go"},
		{Name: "framework", Value: "go test"},
		{Name: "package", Value: one.packageName},
		{Name: "suite", Value: short},
		{Name: "parentSuite", Value: moduleOf(options, one.packageName)},
	}
	if one.test != "" && strings.Contains(one.test, "/") {
		// A subtest keeps its parent as the sub-suite, so one table-driven test
		// that ran twenty cases reads as one suite with twenty results rather
		// than twenty unrelated ones.
		labels = append(labels, label{Name: "subSuite", Value: strings.Split(one.test, "/")[0]})
	}
	for name, value := range options.labels {
		labels = append(labels, label{Name: name, Value: value})
	}
	sort.SliceStable(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
	return labels
}

// moduleOf trims the module path off a package, so the report groups by
// repository rather than by where the code happens to live on this machine.
func moduleOf(options options, packageName string) string {
	if options.module == "" {
		return packageName
	}
	trimmed := strings.TrimPrefix(packageName, options.module)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return options.module
	}
	return options.module
}

// writeEnvironment records the machine and the revision, because a report whose
// numbers cannot be tied to a commit answers a question nobody asked.
func writeEnvironment(options options) error {
	if len(options.environ) == 0 {
		return nil
	}
	names := make([]string, 0, len(options.environ))
	for name := range options.environ {
		names = append(names, name)
	}
	sort.Strings(names)
	text := &strings.Builder{}
	for _, name := range names {
		fmt.Fprintf(text, "%s=%s\n", name, options.environ[name])
	}
	name := filepath.Join(options.out, "environment.properties")
	if err := os.WriteFile(name, []byte(text.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// writeJSON writes one file atomically enough for a build directory: the file
// is either absent or complete, never half-written.
func writeJSON(directory, name string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	encoded = append(encoded, '\n')
	temporary := filepath.Join(directory, "."+name+".tmp")
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(temporary, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("rename %s: %w", name, err)
	}
	return nil
}

// millis is Allure's clock: milliseconds since the epoch, zero meaning unknown.
func millis(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

// identifier derives a stable id. A history id has no time component — it must
// be the same value on every run or every trend line starts over — while a
// result id has one, so two runs of one test are two results rather than one
// overwritten.
func identifier(name string, at time.Time) string {
	sum := sha256.Sum256([]byte(name + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}

// displayName is the last segment of a test name: "TestFoo/case_one" reads as
// "case one", and a package-level result reads as the package.
func displayName(test string) string {
	if test == "" {
		return "(package)"
	}
	segments := strings.Split(test, "/")
	last := segments[len(segments)-1]
	return strings.ReplaceAll(last, "_", " ")
}

// firstLine is the message a reader sees before opening the log: a Go failure
// says what it wanted on the first line that is not a bare indentation of the
// file position.
func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "=== RUN") || strings.HasPrefix(trimmed, "--- FAIL") {
			continue
		}
		if len(trimmed) > 300 {
			trimmed = trimmed[:300] + "…"
		}
		return trimmed
	}
	return ""
}

// parseLabels reads repeated -label name=value flags.
func parseLabels(values []string) (map[string]string, error) {
	labels := make(map[string]string, len(values))
	for _, value := range values {
		name, content, found := strings.Cut(value, "=")
		if !found || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("label %q is not name=value", value)
		}
		labels[strings.TrimSpace(name)] = strings.TrimSpace(content)
	}
	return labels, nil
}

// main is the one statement that cannot be tested: it is the program. Everything
// that decides anything lives in cli, which takes its arguments and its streams
// so a test can hand it a file and read what it says.
func main() {
	os.Exit(cli(os.Args[1:], os.Stdin, os.Stderr))
}

// cli runs the command and returns the process status.
func cli(args []string, stdin io.Reader, stderr io.Writer) int {
	flags := flag.NewFlagSet("allure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		in      = flags.String("in", "", "file holding a `go test -json` stream; empty reads standard input")
		out     = flags.String("out", "build/allure-results", "directory to write Allure results into")
		module  = flags.String("module", "", "module path, used to group results by repository")
		envName = flags.String("environment", "", "file of NAME=value lines describing the run")
		labels  = flags.String("labels", "", "comma-separated name=value labels to attach to every test")
		summary = flags.String("summary", "", "file to write a Markdown summary of the run into; empty writes none")
	)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	source := stdin
	if *in != "" {
		file, err := os.Open(*in)
		if err != nil {
			fmt.Fprintf(stderr, "allure: %v\n", err)
			return 1
		}
		defer file.Close()
		source = file
	}

	labelValues, err := parseLabels(splitNonEmpty(*labels))
	if err != nil {
		fmt.Fprintf(stderr, "allure: %v\n", err)
		return 1
	}
	environ, err := readEnvironment(*envName)
	if err != nil {
		fmt.Fprintf(stderr, "allure: %v\n", err)
		return 1
	}

	results, err := run(options{
		in:       source,
		out:      *out,
		labels:   labelValues,
		module:   *module,
		environ:  environ,
		warnings: stderr,
	})
	if err != nil {
		if !errors.Is(err, os.ErrClosed) {
			fmt.Fprintf(stderr, "allure: %v\n", err)
		}
		return 1
	}
	if *summary != "" {
		if err := os.WriteFile(*summary, []byte(summarize(results)), 0o644); err != nil {
			fmt.Fprintf(stderr, "allure: write %s: %v\n", *summary, err)
			return 1
		}
	}
	return 0
}

// splitNonEmpty splits a comma-separated list without inventing empty entries.
func splitNonEmpty(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// readEnvironment reads NAME=value lines, tolerating blanks and comments.
func readEnvironment(name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	content, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	environ := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		environ[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return environ, nil
}
