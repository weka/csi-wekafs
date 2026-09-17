package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// terminal simulates an interactive session: stdin reports as a TTY and each call to
// readSecret returns the next scripted answer.
func terminal(t *testing.T, answers ...string) *bytes.Buffer {
	t.Helper()
	prompts := captureLogs(t)

	originalIsTerminal, originalReadSecret := stdinIsTerminal, readSecret
	t.Cleanup(func() { stdinIsTerminal, readSecret = originalIsTerminal, originalReadSecret })

	stdinIsTerminal = func() bool { return true }
	readSecret = func() (string, error) {
		if len(answers) == 0 {
			return "", errors.New("readSecret called more times than the test scripted")
		}
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	return prompts
}

// pipedStdin simulates a non-interactive stdin carrying content.
func pipedStdin(t *testing.T, content string) {
	t.Helper()
	originalIsTerminal, originalStdin := stdinIsTerminal, stdin
	t.Cleanup(func() { stdinIsTerminal, stdin = originalIsTerminal, originalStdin })

	stdinIsTerminal = func() bool { return false }
	stdin = strings.NewReader(content)
}

// notATerminal simulates non-interactive stdin with nothing to read.
func notATerminal(t *testing.T) {
	t.Helper()
	pipedStdin(t, "")
}

// TestPasswordStdinOnTerminalFailsFast is the regression test for the reported hang:
//
//	weka-csi-migrator export ... --include-secret-data --password-stdin
//
// with nothing piped in. Reading os.Stdin blocks until EOF, so the command sat there with no
// prompt and no echo handling, looking like it had frozen. It must now refuse immediately.
func TestPasswordStdinOnTerminalFailsFast(t *testing.T) {
	// A TTY that would block forever if anything actually tried to read it.
	originalIsTerminal, originalReadAll := stdinIsTerminal, readAll
	t.Cleanup(func() { stdinIsTerminal, readAll = originalIsTerminal, originalReadAll })

	stdinIsTerminal = func() bool { return true }
	readAll = func(io.Reader) ([]byte, error) {
		t.Fatal("stdin was read even though it is a terminal: this is the hang")
		return nil, nil
	}

	_, err := readPassword(passwordRequest{FromStdin: true, Required: true, Purpose: "to encrypt"})
	if err == nil {
		t.Fatal("--password-stdin on a terminal was accepted")
	}
	// The message has to show the way out, since the failure is otherwise baffling.
	for _, want := range []string{"terminal", "password-stdin", passwordEnvVar} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestPasswordStdinReadsPipedInput(t *testing.T) {
	pipedStdin(t, "piped-secret\n")

	password, err := readPassword(passwordRequest{FromStdin: true, Required: true})
	if err != nil {
		t.Fatalf("readPassword returned error: %v", err)
	}
	if password != "piped-secret" {
		t.Errorf("password = %q, want %q with the trailing newline stripped", password, "piped-secret")
	}
}

func TestPasswordStdinRejectsEmptyPipe(t *testing.T) {
	pipedStdin(t, "")
	if _, err := readPassword(passwordRequest{FromStdin: true, Required: true}); err == nil {
		t.Error("an empty pipe was accepted")
	}
}

func TestEnvironmentPasswordIsUsed(t *testing.T) {
	notATerminal(t)
	t.Setenv(passwordEnvVar, "env-secret")

	password, err := readPassword(passwordRequest{Required: true})
	if err != nil {
		t.Fatalf("readPassword returned error: %v", err)
	}
	if password != "env-secret" {
		t.Errorf("password = %q, want env-secret", password)
	}
}

func TestPasswordStdinAndEnvironmentConflict(t *testing.T) {
	pipedStdin(t, "piped")
	t.Setenv(passwordEnvVar, "env-secret")

	_, err := readPassword(passwordRequest{FromStdin: true, Required: true})
	if err == nil || !strings.Contains(err.Error(), "choose one") {
		t.Errorf("got %v, want a conflict error", err)
	}
}

// TestPromptsWhenRequiredAndInteractive is the behaviour the reported command should have
// had: no piped password, a terminal available, so ask for one.
func TestPromptsWhenRequiredAndInteractive(t *testing.T) {
	prompts := terminal(t, "typed-secret")

	password, err := readPassword(passwordRequest{
		Required: true,
		Prompt:   "Enter a password to encrypt the archive",
	})
	if err != nil {
		t.Fatalf("readPassword returned error: %v", err)
	}
	if password != "typed-secret" {
		t.Errorf("password = %q, want typed-secret", password)
	}
	if !strings.Contains(prompts.String(), "Enter a password to encrypt the archive") {
		t.Errorf("prompt was not shown, got %q", prompts.String())
	}
}

// TestConfirmRequiresMatchingPasswords guards against an archive nobody can ever open.
func TestConfirmRequiresMatchingPasswords(t *testing.T) {
	terminal(t, "secret", "secret")
	password, err := readPassword(passwordRequest{Required: true, Confirm: true, Prompt: "Password"})
	if err != nil {
		t.Fatalf("matching passwords were rejected: %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q, want secret", password)
	}

	terminal(t, "secret", "typo")
	if _, err := readPassword(passwordRequest{Required: true, Confirm: true, Prompt: "Password"}); err == nil {
		t.Error("mismatched confirmation was accepted")
	}
}

func TestPromptRejectsEmptyPassword(t *testing.T) {
	terminal(t, "")
	if _, err := readPassword(passwordRequest{Required: true, Prompt: "Password"}); err == nil {
		t.Error("an empty typed password was accepted")
	}
}

// TestNoPromptWhenNotRequired covers a plain export, which needs no password and must not
// stop to ask for one even on an interactive terminal.
func TestNoPromptWhenNotRequired(t *testing.T) {
	terminal(t) // scripted with no answers: prompting would fail the test

	password, err := readPassword(passwordRequest{Required: false})
	if err != nil {
		t.Fatalf("readPassword returned error: %v", err)
	}
	if password != "" {
		t.Errorf("password = %q, want empty", password)
	}
}

// TestRequiredWithoutTerminalExplainsItself covers CI and scripts, where there is no way to
// prompt and the error is the only guidance available.
func TestRequiredWithoutTerminalExplainsItself(t *testing.T) {
	notATerminal(t)

	_, err := readPassword(passwordRequest{Required: true, Purpose: "to encrypt the archive"})
	if err == nil {
		t.Fatal("a required password was skipped on a non-interactive stdin")
	}
	for _, want := range []string{"to encrypt the archive", passwordEnvVar, "--password-stdin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestPromptGoesToStderr keeps prompting compatible with `export --output -`, which streams
// the archive down stdout while the operator is being asked for a password.
func TestPromptGoesToStderr(t *testing.T) {
	prompts := terminal(t, "secret")

	if _, err := readPassword(passwordRequest{Required: true, Prompt: "Enter the password"}); err != nil {
		t.Fatalf("readPassword returned error: %v", err)
	}
	if !strings.Contains(prompts.String(), "Enter the password") {
		t.Error("the prompt did not reach the captured stderr writer")
	}
}
