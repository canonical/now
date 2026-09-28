// Copyright 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/canonical/now/internal/cli"
)

func approve(t *testing.T, opts cli.ApprovalOptions) (bool, string) {
	t.Helper()
	var stderr strings.Builder
	opts.Stderr = &stderr
	ok, err := cli.Approve(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ok, stderr.String()
}

func TestApproveYes(t *testing.T) {
	// -y approves without asking; the ENTER separator and the blank
	// line print, matching the interactive path where the user's
	// ENTER echoes a newline.
	ok, out := approve(t, cli.ApprovalOptions{Script: "the script", Yes: true})
	assertEqual(t, "approved", ok, true)
	assertEqual(t, "stderr", out, "the script\n[ ENTER | CTRL-C ]\n\n")
}

func TestApproveQuiet(t *testing.T) {
	// -q approves without showing the script, and the separator is
	// suppressed too: nothing is produced.
	ok, out := approve(t, cli.ApprovalOptions{Script: "the script", Quiet: true})
	assertEqual(t, "approved", ok, true)
	assertEqual(t, "stderr", out, "")
}

func TestApproveEnter(t *testing.T) {
	// ENTER (an empty line) approves.
	ok, out := approve(t, cli.ApprovalOptions{Script: "the script", TTY: strings.NewReader("\n")})
	assertEqual(t, "approved", ok, true)
	if !strings.Contains(out, "the script") || !strings.Contains(out, "[ ENTER | CTRL-C ]") {
		t.Errorf("script or prompt missing: %q", out)
	}
}

func TestApproveOtherInputCancels(t *testing.T) {
	// Any non-empty input cancels.
	ok, _ := approve(t, cli.ApprovalOptions{Script: "the script", TTY: strings.NewReader("n\n")})
	assertEqual(t, "approved", ok, false)
}

func TestApproveEOFCancels(t *testing.T) {
	ok, _ := approve(t, cli.ApprovalOptions{Script: "the script", TTY: strings.NewReader("")})
	assertEqual(t, "approved", ok, false)
}

func TestApproveNoTerminalRejects(t *testing.T) {
	// Without a controlling terminal there is no way to ask, so
	// approval fails as an error; the script is still shown for review.
	// Only meaningful in environments without a tty.
	if _, err := os.Open("/dev/tty"); err == nil {
		t.Skip("environment has a controlling terminal")
	}
	var stderr strings.Builder
	ok, err := cli.Approve(context.Background(), cli.ApprovalOptions{Script: "the script", Stderr: &stderr})
	assertEqual(t, "approved", ok, false)
	if err == nil || !strings.Contains(err.Error(), "cannot ask for approval") {
		t.Fatalf("expected approval error, got %v", err)
	}
	if !strings.Contains(stderr.String(), "the script") {
		t.Errorf("script not shown: %q", stderr.String())
	}
}
