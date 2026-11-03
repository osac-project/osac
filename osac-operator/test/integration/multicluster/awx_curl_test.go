/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package multicluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
)

// --- Fake kubectl helpers ---

// writeFakeKubectl creates a deterministic fake kubectl that
// captures argv and stdin to files, then responds with the
// given JSON on stdout.  Empty responseJSON exits 1.
func writeFakeKubectl(
	t *testing.T, dir, responseJSON string,
) (argvFile, stdinFile string) {
	t.Helper()
	argvFile = dir + "/argv_captured.txt"
	stdinFile = dir + "/stdin_captured.txt"
	script := fmt.Sprintf(`#!/bin/sh
set -e
for arg in "$@"; do echo "$arg"; done > %s
cat > %s
`, argvFile, stdinFile)
	if responseJSON != "" {
		script += fmt.Sprintf(
			"printf '%%s\\n' '%s'\n",
			strings.ReplaceAll(responseJSON, "'", "'\\''"))
	} else {
		script += "echo 'simulated failure' >&2\nexit 1\n"
	}
	if err := os.WriteFile(dir+"/kubectl",
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	return argvFile, stdinFile
}

func readCaptured(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	return string(b)
}

// --- Gate A: Core runAWXCurl tests ---

// TestAWXCurlTokenNotInArgv verifies token goes through stdin,
// not argv; response round-trips; output is redacted.
func TestAWXCurlTokenNotInArgv(t *testing.T) {
	sentinel := "SENTINEL-TOKEN-VALUE-12345"
	fakeDir := t.TempDir()
	argvFile, stdinFile := writeFakeKubectl(t, fakeDir,
		`{"results":[],"count":0}`)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXCurl(sentinel,
		"http://awx.test/api/v2/projects/",
		"GET", "", "test-namespace")
	if err != nil {
		t.Fatalf("must succeed; got: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}

	argv := readCaptured(t, argvFile)
	if strings.Contains(argv, sentinel) {
		t.Fatalf("argv must not contain token:\n%s", argv)
	}
	if !strings.Contains(argv, "--config") ||
		!strings.Contains(argv, "awx.test") {
		t.Fatalf("argv missing expected args:\n%s", argv)
	}

	if strings.Contains(string(out), sentinel) {
		t.Fatalf("output must not contain token")
	}

	stdin := readCaptured(t, stdinFile)
	if !strings.Contains(stdin,
		"Authorization: Bearer "+sentinel) {
		t.Fatalf("stdin must contain auth header:\n%s",
			stdin)
	}
}

// TestAWXCurlPostRoundTrip verifies POST method, body, and
// Content-Type in argv; token only in stdin.
func TestAWXCurlPostRoundTrip(t *testing.T) {
	sentinel := "SENTINEL-TOKEN-POST-67890"
	body := `{"name":"test","organization":1}`
	fakeDir := t.TempDir()
	argvFile, stdinFile := writeFakeKubectl(t, fakeDir,
		`{"id":42}`)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXPost(sentinel,
		"http://awx.test/api/v2/inventories/", body,
		"test-namespace")
	if err != nil {
		t.Fatalf("must succeed; got: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if resp["id"] != float64(42) {
		t.Fatalf("expected id=42, got %v", resp["id"])
	}

	argv := readCaptured(t, argvFile)
	for _, must := range []string{
		"-X", "POST", "-H",
		"Content-Type: application/json",
		"-d", body,
		"http://awx.test/api/v2/inventories/",
	} {
		if !strings.Contains(argv, must) {
			t.Fatalf("argv missing %q:\n%s", must, argv)
		}
	}
	if strings.Contains(argv, sentinel) {
		t.Fatalf("argv must not contain token")
	}

	stdin := readCaptured(t, stdinFile)
	if !strings.Contains(stdin, sentinel) {
		t.Fatalf("stdin must contain token")
	}
}

// TestAWXCurlErrorReturnsNil verifies error returns nil output
// and the error does not contain the token.
func TestAWXCurlErrorReturnsNil(t *testing.T) {
	sentinel := "SENTINEL-TOKEN-FAIL-99999"
	fakeDir := t.TempDir()
	argvFile, _ := writeFakeKubectl(t, fakeDir, "") // fail
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXGet(sentinel,
		"http://awx.test/api/v2/projects/", "test-ns")
	if err == nil {
		t.Fatal("expected error")
	}
	if out != nil {
		t.Fatalf("output on error must be nil; got %d bytes",
			len(out))
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error must not contain token: %s",
			err.Error())
	}

	argv := readCaptured(t, argvFile)
	if len(argv) == 0 {
		t.Fatal("fake did not run")
	}
	if strings.Contains(argv, sentinel) {
		t.Fatalf("argv must not contain token on failure")
	}
}

// TestAWXCurlTokenRedactedFromResponse verifies known token
// and generic bearer patterns are redacted from returned output.
func TestAWXCurlTokenRedactedFromResponse(t *testing.T) {
	sentinel := "SENTINEL-ECHO-TOKEN-44444"
	echoResp := fmt.Sprintf(
		`{"token":"%s","auth":"Bearer %s","status":"ok"}`,
		sentinel, sentinel)
	fakeDir := t.TempDir()
	writeFakeKubectl(t, fakeDir, echoResp)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXGet(sentinel,
		"http://awx.test/api/v2/echo/", "test-ns")
	if err != nil {
		t.Fatalf("should succeed; got: %v", err)
	}
	if strings.Contains(string(out), sentinel) {
		t.Fatalf("returned output must redact known "+
			"token:\n%s", out)
	}
	if !strings.Contains(string(out), "[REDACTED]") {
		t.Fatalf("output must contain [REDACTED]:\n%s",
			out)
	}
}

// TestAWXCurlRejectsControlCharsInToken verifies rejection of
// CR, LF, NUL, quotes, backslash, DEL, tab, and empty tokens.
func TestAWXCurlRejectsControlCharsInToken(t *testing.T) {
	for _, tc := range []struct {
		name, token string
	}{
		{"newline", "token\ninjection"},
		{"carriage return", "token\rinjection"},
		{"null byte", "token\x00injection"},
		{"double quote", `token"injection`},
		{"backslash", `token\injection`},
		{"tab", "token\tinjection"},
		{"DEL", "token\x7finjection"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runAWXCurl(tc.token,
				"http://awx.test/api/v2/test/",
				"GET", "", "test-ns")
			if err == nil {
				t.Fatal("must reject invalid token")
			}
			if tc.token != "" &&
				strings.Contains(err.Error(), tc.token) {
				t.Fatalf("error must not echo token: %s",
					err.Error())
			}
		})
	}
}

// TestAWXCurlPatchMethod verifies PATCH requests.
func TestAWXCurlPatchMethod(t *testing.T) {
	sentinel := "SENTINEL-TOKEN-PATCH-11111"
	body := `{"scm_branch":"main","scm_refspec":""}`
	fakeDir := t.TempDir()
	argvFile, _ := writeFakeKubectl(t, fakeDir, `{"ok":true}`)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	_, err := runAWXPatch(sentinel,
		"http://awx.test/api/v2/projects/1/", body, "test-ns")
	if err != nil {
		t.Fatalf("must succeed; got: %v", err)
	}

	argv := readCaptured(t, argvFile)
	if !strings.Contains(argv, "PATCH") ||
		!strings.Contains(argv, body) {
		t.Fatalf("argv must contain PATCH and body:\n%s",
			argv)
	}
	if strings.Contains(argv, sentinel) {
		t.Fatalf("argv must not contain token")
	}
}

// TestAWXCurlOversizedStdoutReturnsNil verifies that >2MiB
// stdout returns nil output and an error.
func TestAWXCurlOversizedStdoutReturnsNil(t *testing.T) {
	// Generate a response > maxAWXResponseBytes (2 MiB).
	bigPayload := strings.Repeat("X", maxAWXResponseBytes+1024)
	fakeDir := t.TempDir()
	// Write a fake that outputs the big payload.
	fakePath := fakeDir + "/kubectl"
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
dd if=/dev/zero bs=1 count=%d 2>/dev/null | tr '\0' 'X'
`, maxAWXResponseBytes+1024)
	_ = bigPayload // sizing reference only
	if err := os.WriteFile(fakePath,
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXGet("valid-token-abc",
		"http://awx.test/api/v2/big/", "test-ns")
	if err == nil {
		t.Fatal("must return error on truncated stdout")
	}
	if out != nil {
		t.Fatalf("output must be nil on truncation; "+
			"got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error must mention truncated: %s",
			err.Error())
	}
}

// TestAWXCurlBoundedStderr verifies that stderr over 64KiB
// returns a truncation error (not raw partial output).
func TestAWXCurlBoundedStderr(t *testing.T) {
	fakeDir := t.TempDir()
	fakePath := fakeDir + "/kubectl"
	// Produce >64KiB stderr + valid stdout.
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
dd if=/dev/zero bs=1 count=%d 2>/dev/null | tr '\0' 'E' >&2
echo '{"ok":true}'
`, maxAWXStderrBytes+1024)
	if err := os.WriteFile(fakePath,
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXGet("valid-token-abc",
		"http://awx.test/api/v2/noisy/", "test-ns")
	if err == nil {
		t.Fatal("must error on truncated stderr")
	}
	if out != nil {
		t.Fatalf("output must be nil on stderr truncation;"+
			" got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error must mention truncated: %s",
			err.Error())
	}
}

// TestAWXCurlShortDeadline verifies that a command exceeding a
// short deadline is terminated and returns a timeout error,
// using runAWXCurlWithContext with a sub-second context.
func TestAWXCurlShortDeadline(t *testing.T) {
	fakeDir := t.TempDir()
	// exec replaces the shell with sleep so SIGKILL from
	// CommandContext terminates it directly.
	if err := os.WriteFile(fakeDir+"/kubectl", []byte(
		"#!/bin/sh\ncat > /dev/null\nexec sleep 300\n",
	), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(
		context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := runAWXCurlWithContext(ctx,
		"valid-token-abc",
		"http://awx.test/api/v2/slow/",
		"GET", "", "test-ns")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("must error on timeout")
	}
	if out != nil {
		t.Fatal("output must be nil on timeout")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("must terminate quickly; took %v",
			elapsed)
	}
}

// TestAWXCurlNakedTokenRedactedInSuccessResponse verifies
// that the known token WITHOUT "Bearer" prefix is also redacted.
func TestAWXCurlNakedTokenRedactedInSuccessResponse(t *testing.T) {
	sentinel := "NAKED-TOKEN-SENTINEL-22222"
	resp := fmt.Sprintf(`{"debug":"%s","ok":true}`, sentinel)
	fakeDir := t.TempDir()
	writeFakeKubectl(t, fakeDir, resp)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	out, err := runAWXGet(sentinel,
		"http://awx.test/api/v2/echo/", "test-ns")
	if err != nil {
		t.Fatalf("must succeed; got: %v", err)
	}
	if strings.Contains(string(out), sentinel) {
		t.Fatalf("naked token must be redacted from "+
			"success output:\n%s", out)
	}
}

// TestSanitizeOutputRedactsBearer verifies the generic redactor.
func TestSanitizeOutputRedactsBearer(t *testing.T) {
	input := "GET /api\n" +
		"Authorization: Bearer eyJhbGciOiJ9.secret\n" +
		"more output"
	result := sanitizeOutput(input)
	if strings.Contains(result, "eyJhbGciOiJ9") {
		t.Fatalf("must redact: %s", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("must contain [REDACTED]: %s", result)
	}
	if !strings.Contains(result, "GET /api") ||
		!strings.Contains(result, "more output") {
		t.Fatalf("must preserve benign: %s", result)
	}
}

// --- Gate B: Diagnostic test with GinkgoWriter capture ---

// TestDumpFailedProjectUpdateStdoutDiagnostic drives the REAL
// dumpFailedProjectUpdateStdout through a stateful fake,
// captures GinkgoWriter via TeeTo, and asserts both API paths,
// benign content, REDACTED markers, and no sentinel in logs.
func TestDumpFailedProjectUpdateStdoutDiagnostic(t *testing.T) {
	sentinel := "SENTINEL-DIAG-TOKEN-55555"

	fakeDir := t.TempDir()
	argvLog := fakeDir + "/argv_log.txt"
	script := fmt.Sprintf(`#!/bin/sh
echo "---CALL---" >> %s
for arg in "$@"; do echo "$arg"; done >> %s
cat > /dev/null
last_arg=$(eval echo "\${$#}")
case "$last_arg" in
  *project_updates/*stdout*)
    echo "Cloning into repo..."
    echo "fatal: could not read Username"
    echo "leaked-token: %s"
    ;;
  *project_updates*)
    echo '{"results":[{"id":99,"status":"failed"}]}'
    ;;
  *)
    echo '{"error":"unexpected"}'
    exit 1
    ;;
esac
`, argvLog, argvLog, sentinel)

	if err := os.WriteFile(fakeDir+"/kubectl",
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	// Capture GinkgoWriter output via TeeTo.
	var logBuf bytes.Buffer
	GinkgoWriter.TeeTo(&logBuf)
	t.Cleanup(func() { GinkgoWriter.ClearTeeWriters() })

	// Call the REAL production function.
	dumpFailedProjectUpdateStdout(
		"http://awx.test/api/v2", sentinel, 42)

	logged := logBuf.String()

	// Assert two API calls happened.
	log := readCaptured(t, argvLog)
	if strings.Count(log, "---CALL---") != 2 {
		t.Fatalf("expected 2 API calls:\n%s", log)
	}
	if !strings.Contains(log,
		"project_updates/?project=42") {
		t.Fatalf("first call must query project=42:\n%s",
			log)
	}
	if !strings.Contains(log,
		"project_updates/99/stdout") {
		t.Fatalf("second call must fetch update 99:\n%s",
			log)
	}
	if strings.Contains(log, sentinel) {
		t.Fatalf("token must not be in any argv:\n%s", log)
	}

	// Assert GinkgoWriter logged output.
	if !strings.Contains(logged, "update 99") {
		t.Fatalf("log must contain update ID 99:\n%s",
			logged)
	}
	if !strings.Contains(logged,
		"could not read Username") {
		t.Fatalf("log must contain benign failure text:"+
			"\n%s", logged)
	}
	if strings.Contains(logged, sentinel) {
		t.Fatalf("log must not contain sentinel token:"+
			"\n%s", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Fatalf("log must contain [REDACTED]:\n%s",
			logged)
	}
}

// TestAWXCurlFailureWithTokenInStdoutStderr verifies that when
// the subprocess fails AND echoes the token in both stdout and
// stderr, the caller receives nil output and a safe error.
func TestAWXCurlFailureWithTokenInStdoutStderr(t *testing.T) {
	sentinel := "SENTINEL-LEAK-TOKEN-88888"
	fakeDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
echo "leaked stdout: %s"
echo "leaked stderr: %s" >&2
exit 1
`, sentinel, sentinel)
	if err := os.WriteFile(fakeDir+"/kubectl",
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	var logBuf bytes.Buffer
	GinkgoWriter.TeeTo(&logBuf)
	t.Cleanup(func() { GinkgoWriter.ClearTeeWriters() })

	out, err := runAWXGet(sentinel,
		"http://awx.test/api/v2/fail/", "test-ns")
	if err == nil {
		t.Fatal("expected error")
	}
	if out != nil {
		t.Fatalf("output on failure must be nil; got %d",
			len(out))
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error must not contain token: %s",
			err.Error())
	}

	logged := logBuf.String()
	if strings.Contains(logged, sentinel) {
		t.Fatalf("GinkgoWriter log must not contain "+
			"token:\n%s", logged)
	}
}

// --- Gate C: Real curl roundtrip via httptest ---

// requestRecord captures what the httptest server received.
type requestRecord struct {
	Method string
	URL    string
	Auth   string
	Body   string
}

// TestAWXCurlRealCurlRoundTrip starts a local HTTP server and
// uses a fake kubectl that delegates to the actual curl binary.
// Verifies Authorization header, method, URL, and body arrive
// correctly for GET, POST, and PATCH.  Token never in argv.
func TestAWXCurlRealCurlRoundTrip(t *testing.T) {
	curlPath, err := exec.LookPath("curl")
	if err != nil {
		t.Fatalf("curl is required for this test: %v", err)
	}

	sentinel := "SENTINEL-CURL-REAL-33333"

	// Channel-based request recording for safe synchronization.
	received := make(chan requestRecord, 1)
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter,
			r *http.Request) {
			bodyBytes, err := io.ReadAll(
				io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			received <- requestRecord{
				Method: r.Method,
				URL:    r.URL.String(),
				Auth:   r.Header.Get("Authorization"),
				Body:   string(bodyBytes),
			}
			w.Header().Set("Content-Type",
				"application/json")
			fmt.Fprintf(w, `{"received":true}`)
		}))
	defer srv.Close()

	// Build a fake kubectl that captures argv, strips the
	// leading remote command name ("curl") after "--", then
	// execs the REAL curl binary with only its flags/args.
	fakeDir := t.TempDir()
	argvLog := fakeDir + "/argv_log.txt"
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do echo "$arg"; done > %s
# Skip kubectl exec args up to and including "--"
while [ $# -gt 0 ] && [ "$1" != "--" ]; do shift; done
shift  # skip "--"
# $1 is now the remote command name ("curl") — validate
# and strip it so it is not forwarded as an extra URL.
if [ "$1" != "curl" ]; then
  echo "expected remote command 'curl', got '$1'" >&2
  exit 1
fi
shift  # strip "curl"
# Run the REAL curl binary with only its own args.
exec env NO_PROXY="*" '%s' "$@"
`, argvLog, curlPath)

	if err := os.WriteFile(fakeDir+"/kubectl",
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	cases := []struct {
		name   string
		method string
		url    string
		body   string
		call   func() ([]byte, error)
	}{
		{
			name:   "POST with complex body",
			method: "POST",
			url:    "/api/v2/test/?query=yes",
			body: `{"key":"value with \"quotes\"",` +
				`"multiline":"line1\nline2"}`,
			call: func() ([]byte, error) {
				return runAWXPost(sentinel,
					srv.URL+"/api/v2/test/?query=yes",
					`{"key":"value with \"quotes\"",`+
						`"multiline":"line1\nline2"}`,
					"test-ns")
			},
		},
		{
			name:   "GET",
			method: "GET",
			url:    "/api/v2/get/",
			body:   "",
			call: func() ([]byte, error) {
				return runAWXGet(sentinel,
					srv.URL+"/api/v2/get/", "test-ns")
			},
		},
		{
			name:   "PATCH with body",
			method: "PATCH",
			url:    "/api/v2/projects/1/",
			body:   `{"scm_branch":"pr-head"}`,
			call: func() ([]byte, error) {
				return runAWXPatch(sentinel,
					srv.URL+"/api/v2/projects/1/",
					`{"scm_branch":"pr-head"}`,
					"test-ns")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.call()
			if err != nil {
				t.Fatalf("roundtrip must succeed: %v",
					err)
			}

			// Receive server-side record.
			rec := <-received

			if rec.Method != tc.method {
				t.Fatalf("server method=%s, want %s",
					rec.Method, tc.method)
			}
			if rec.URL != tc.url {
				t.Fatalf("server URL=%s, want %s",
					rec.URL, tc.url)
			}
			if rec.Auth != "Bearer "+sentinel {
				t.Fatalf("server auth=%q, want Bearer",
					rec.Auth)
			}
			if rec.Body != tc.body {
				t.Fatalf("server body mismatch:"+
					"\ngot:  %s\nwant: %s",
					rec.Body, tc.body)
			}

			// Response must be valid JSON.
			var resp map[string]interface{}
			if err := json.Unmarshal(out, &resp); err != nil {
				t.Fatalf("response not JSON: %v", err)
			}

			// Token must NOT be in argv.
			argv := readCaptured(t, argvLog)
			if strings.Contains(argv, sentinel) {
				t.Fatalf("argv must not contain "+
					"token:\n%s", argv)
			}
		})
	}
}

// TestAWXCurlRejectInvalidTokenNoFakeInvocation verifies that
// invalid tokens are rejected BEFORE any subprocess is spawned.
func TestAWXCurlRejectInvalidTokenNoFakeInvocation(t *testing.T) {
	fakeDir := t.TempDir()
	invocationMarker := fakeDir + "/invoked.txt"
	script := fmt.Sprintf(`#!/bin/sh
echo "INVOKED" > %s
echo '{}'
`, invocationMarker)
	if err := os.WriteFile(fakeDir+"/kubectl",
		[]byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	_, err := runAWXCurl("token\ninjection",
		"http://awx.test/", "GET", "", "test-ns")
	if err == nil {
		t.Fatal("must reject invalid token")
	}

	if _, statErr := os.Stat(invocationMarker); statErr == nil {
		t.Fatal("fake kubectl was invoked — rejection " +
			"must happen before exec")
	}
}
