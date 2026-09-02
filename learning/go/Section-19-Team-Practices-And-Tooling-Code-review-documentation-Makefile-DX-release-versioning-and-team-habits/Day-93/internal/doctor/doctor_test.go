package doctor_test

import (
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/doctor"
)

func TestGoVersionPassesOnAModernToolchain(t *testing.T) {
	report := doctor.Run(t.Context(), doctor.Options{MinGoMinor: 22})

	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}

	if report.Results[0].Status != doctor.OK {
		t.Errorf("go version check = %+v, want ok", report.Results[0])
	}
}

func TestGoVersionFailsWhenTooOld(t *testing.T) {
	// A minor version no toolchain will ever reach.
	report := doctor.Run(t.Context(), doctor.Options{MinGoMinor: 9999})

	if !report.Failed() {
		t.Error("an impossible minimum version did not fail")
	}

	if report.Results[0].Fix == "" {
		t.Error("the failure carries no fix, which is the half that matters")
	}
}

// A missing tool is a WARNING: the code still builds and the tests still run,
// so it must not stop the setup script.
func TestMissingToolIsAWarningNotAFailure(t *testing.T) {
	report := doctor.Run(t.Context(), doctor.Options{
		MinGoMinor: 22,
		Tools:      map[string]string{"definitely-not-installed-xyz": "install it"},
	})

	found := false

	for _, result := range report.Results {
		if strings.Contains(result.Name, "definitely-not-installed") {
			found = true

			if result.Status != doctor.Warn {
				t.Errorf("status = %s, want warn", result.Status)
			}

			if result.Fix != "install it" {
				t.Errorf("fix = %q", result.Fix)
			}
		}
	}

	if !found {
		t.Fatal("the missing tool was not reported")
	}

	if report.Failed() {
		t.Error("a missing optional tool must not fail the whole check")
	}
}

func TestEnvironmentVariables(t *testing.T) {
	t.Setenv("DOCTOR_TEST_SET", "value")

	report := doctor.Run(t.Context(), doctor.Options{
		MinGoMinor: 22,
		EnvVars: map[string]string{
			"DOCTOR_TEST_SET":   "should be reported as ok",
			"DOCTOR_TEST_UNSET": "should be reported as a warning",
		},
	})

	statuses := make(map[string]doctor.Status)

	for _, result := range report.Results {
		statuses[result.Name] = result.Status
	}

	if statuses["DOCTOR_TEST_SET"] != doctor.OK {
		t.Errorf("set variable = %s, want ok", statuses["DOCTOR_TEST_SET"])
	}

	if statuses["DOCTOR_TEST_UNSET"] != doctor.Warn {
		t.Errorf("unset variable = %s, want warn", statuses["DOCTOR_TEST_UNSET"])
	}
}

func TestPortInUseIsReported(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	report := doctor.Run(t.Context(), doctor.Options{MinGoMinor: 22, Port: listener.Addr().String()})

	last := report.Results[len(report.Results)-1]

	if last.Status != doctor.Warn {
		t.Errorf("busy port = %+v, want a warning", last)
	}

	if !strings.Contains(last.Detail, "in use") {
		t.Errorf("detail = %q", last.Detail)
	}
}

func TestDatabaseChecks(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent.db")

	report := doctor.Run(t.Context(), doctor.Options{MinGoMinor: 22, Database: missing})

	if last := report.Results[len(report.Results)-1]; last.Status != doctor.Warn {
		t.Errorf("missing database = %+v, want a warning suggesting make migrate", last)
	}

	// An existing but unmigrated database is also a warning, with a different
	// fix - "make migrate" rather than "make setup".
	unmigrated := filepath.Join(dir, "empty.db")

	db, err := sql.Open("sqlite", "file:"+unmigrated)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE placeholder (id INTEGER);`); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	report = doctor.Run(t.Context(), doctor.Options{MinGoMinor: 22, Database: unmigrated})

	last := report.Results[len(report.Results)-1]

	if last.Status != doctor.Warn || !strings.Contains(last.Fix, "migrate") {
		t.Errorf("unmigrated database = %+v", last)
	}
}

func TestCountsAndFailed(t *testing.T) {
	report := doctor.Report{Results: []doctor.Result{
		{Status: doctor.OK}, {Status: doctor.OK}, {Status: doctor.Warn},
	}}

	ok, warn, fail := report.Counts()

	if ok != 2 || warn != 1 || fail != 0 {
		t.Errorf("counts = (%d, %d, %d), want (2, 1, 0)", ok, warn, fail)
	}

	if report.Failed() {
		t.Error("Failed() reported true with no failures")
	}

	report.Results = append(report.Results, doctor.Result{Status: doctor.Fail})

	if !report.Failed() {
		t.Error("Failed() reported false with a failure present")
	}
}

// Every non-ok result must carry a fix. A diagnostic that names a problem and
// stops has done half the job.
func TestEveryProblemCarriesAFix(t *testing.T) {
	report := doctor.Run(t.Context(), doctor.Options{
		MinGoMinor: 22,
		Tools:      map[string]string{"definitely-not-installed-xyz": "install it"},
		EnvVars:    map[string]string{"DOCTOR_TEST_UNSET_2": "why it matters"},
		Database:   filepath.Join(t.TempDir(), "absent.db"),
	})

	for _, result := range report.Results {
		if result.Status != doctor.OK && result.Fix == "" {
			t.Errorf("%+v has no fix", result)
		}
	}

	_ = os.Getenv("PATH")
}
