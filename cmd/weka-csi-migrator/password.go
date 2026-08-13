package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// passwordEnvVar is the only environment variable the tool reads a password from. Passing a
// password as a command-line argument is deliberately unsupported: argv is visible to every
// process on the host and is routinely captured in shell history.
const passwordEnvVar = "WEKA_CSI_MIGRATOR_PASSWORD"

// Indirections so that tests can drive the terminal paths without a real TTY.
var (
	stdin           io.Reader = os.Stdin
	stdinIsTerminal           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	// readSecret reads a line with echo disabled, so a password never appears on screen or
	// in a scrollback buffer.
	readSecret = func() (string, error) {
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		return string(secret), err
	}
)

// passwordRequest describes how a password should be obtained. It is a struct rather than a
// set of bare bools so that transposing two of them cannot silently flip whether a password
// is mandatory — which would be the difference between refusing to run and writing an
// unencrypted archive full of credentials.
type passwordRequest struct {
	// FromStdin reads the password from standard input, for non-interactive use.
	FromStdin bool
	// Required means the operation cannot proceed without a password. Only a required
	// request will prompt; a plain export needs no password and must not stop to ask.
	Required bool
	// Purpose completes the sentence "a password is required …" in the error message.
	Purpose string
	// Prompt is the interactive prompt, without trailing punctuation.
	Prompt string
	// Confirm asks a second time and requires the two to match. Use it whenever a typo
	// would be unrecoverable, which is any password that encrypts something.
	Confirm bool
}

// readPassword resolves a password from stdin, the environment, or an interactive prompt.
func readPassword(req passwordRequest) (string, error) {
	envPassword, envSet := os.LookupEnv(passwordEnvVar)

	switch {
	case req.FromStdin && envSet:
		return "", fmt.Errorf("both --password-stdin and %s were given; choose one", passwordEnvVar)

	case req.FromStdin:
		// Reading a terminal here would block forever with no prompt, which looks exactly
		// like a hang. Fail immediately and say how to fix it.
		if stdinIsTerminal() {
			return "", fmt.Errorf("--password-stdin expects the password to be piped in, but stdin is a terminal\n"+
				"  pipe it:   echo -n 'password' | weka-csi-migrator ... --password-stdin\n"+
				"  or set:    %s=... weka-csi-migrator ...\n"+
				"  or drop --password-stdin to be prompted", passwordEnvVar)
		}
		password, err := readAllStdin()
		if err != nil {
			return "", err
		}
		if password == "" {
			return "", errors.New("--password-stdin was given but stdin was empty")
		}
		return password, nil

	case envSet:
		if envPassword == "" {
			return "", fmt.Errorf("%s is set but empty", passwordEnvVar)
		}
		return envPassword, nil

	case req.Required && stdinIsTerminal():
		return promptPassword(req)

	case req.Required:
		return "", fmt.Errorf("a password is required %s, and stdin is not a terminal so it cannot be prompted for\n"+
			"  set %s, or pass --password-stdin and pipe the password in", req.Purpose, passwordEnvVar)

	default:
		return "", nil
	}
}

// promptPassword asks interactively with echo disabled.
func promptPassword(req passwordRequest) (string, error) {
	prompt := req.Prompt
	if prompt == "" {
		prompt = "Password"
	}

	password, err := askSecret(prompt)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("password may not be empty")
	}
	if !req.Confirm {
		return password, nil
	}

	confirmation, err := askSecret("Confirm password")
	if err != nil {
		return "", err
	}
	if password != confirmation {
		// Refusing here costs one retry. Accepting a typo would produce an archive nobody
		// can ever open, and the mistake would only surface at restore time.
		return "", errors.New("passwords do not match")
	}
	return password, nil
}

// askSecret writes a prompt to stderr and reads one line without echoing it.
//
// The prompt goes to stderr, never stdout, so that `export --output -` can still stream an
// archive down a pipe while asking the operator for a password.
func askSecret(prompt string) (string, error) {
	if _, err := fmt.Fprintf(stderr, "%s: ", prompt); err != nil {
		return "", fmt.Errorf("writing password prompt: %w", err)
	}
	secret, err := readSecret()
	// ReadPassword swallows the newline the user typed, so emit one to keep later output
	// from starting mid-line.
	_, _ = fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimRight(secret, "\r\n"), nil
}

func readAllStdin() (string, error) {
	data, err := readAll(stdin)
	if err != nil {
		return "", fmt.Errorf("reading password from stdin: %w", err)
	}
	// A password piped with `echo` carries a trailing newline that the user did not intend.
	return strings.TrimRight(string(data), "\r\n"), nil
}

// readAll is a thin indirection so that tests can exercise password handling.
var readAll = io.ReadAll
