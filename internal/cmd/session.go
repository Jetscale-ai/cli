package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type sessionContextKey struct{}
type sessionTargetKey struct{}

type sessionTarget struct {
	instance string
	origin   string
}

type commandSessionState struct {
	session          *auth.Session
	instance         string
	ephemeral        bool
	fresh            bool
	verifiedIdentity *auth.WhoamiResult
}

// Cleanup belongs around RunE, not PostRunE: Cobra skips post hooks on errors.
func wrapSessionLifecycle(command *cobra.Command) {
	if run := command.RunE; run != nil {
		command.RunE = func(cmd *cobra.Command, args []string) (err error) {
			if err := validateStdinAuthFlags(cmd); err != nil {
				return err
			}
			if flagPasswordStdin {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				cmd.SetContext(ctx)
			}
			state := &commandSessionState{}
			cmd.SetContext(context.WithValue(cmd.Context(), sessionContextKey{}, state))
			defer func() {
				if state.session == nil {
					return
				}
				if state.ephemeral {
					if cleanupErr := cleanupCommandSession(state.session); cleanupErr != nil {
						err = errors.Join(err, fmt.Errorf("temporary session cleanup failed: %w", cleanupErr))
					} else {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Temporary session signed out; no credentials saved.")
					}
				} else {
					saveErr := state.session.Save(state.instance)
					err = errors.Join(err, saveErr)
					if saveErr != nil && state.fresh {
						if cleanupErr := cleanupCommandSession(state.session); cleanupErr != nil {
							err = errors.Join(err, fmt.Errorf("new login cleanup failed: %w", cleanupErr))
						} else {
							_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "New login signed out because credentials could not be saved.")
						}
					}
				}
				state.session.HTTPClient.CloseIdleConnections()
			}()
			if flagPasswordStdin {
				instance, apiURL, err := resolveSessionTarget(cmd)
				if err != nil {
					return err
				}
				if _, err := commandSession(cmd, instance, apiURL); err != nil {
					return err
				}
			}
			if flagPasswordStdin && cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			err = run(cmd, args)
			if flagPasswordStdin {
				err = errors.Join(err, cmd.Context().Err())
			}
			return err
		}
	}
	for _, child := range command.Commands() {
		wrapSessionLifecycle(child)
	}
}

func resolveSessionTarget(cmd *cobra.Command) (string, string, error) {
	if target, ok := cmd.Context().Value(sessionTargetKey{}).(sessionTarget); ok {
		return target.instance, target.origin, nil
	}
	return resolveAPI()
}

func cleanupCommandSession(session *auth.Session) error {
	ctx, cancel := context.WithTimeout(context.Background(), temporarySignOutTimeout)
	defer cancel()
	return session.SignOutContext(ctx)
}

func rememberSession(cmd *cobra.Command, session *auth.Session, instance string, ephemeral bool) {
	state := cmd.Context().Value(sessionContextKey{}).(*commandSessionState)
	state.session, state.instance, state.ephemeral = session, instance, ephemeral
}

func commandIdentity(cmd *cobra.Command, session *auth.Session) (auth.WhoamiResult, error) {
	if err := cmd.Context().Err(); err != nil {
		return auth.WhoamiResult{}, err
	}
	if state, ok := cmd.Context().Value(sessionContextKey{}).(*commandSessionState); ok && state.verifiedIdentity != nil {
		return *state.verifiedIdentity, nil
	}
	return session.WhoamiContext(cmd.Context())
}

func commandSession(cmd *cobra.Command, instance, apiURL string) (*auth.Session, error) {
	state := cmd.Context().Value(sessionContextKey{}).(*commandSessionState)
	if state.session != nil {
		if !state.session.MatchesOrigin(apiURL) {
			return nil, errors.New("cached session does not match the requested API origin and channel; command not run")
		}
		return state.session, nil
	}
	var session *auth.Session
	var err error
	if flagPasswordStdin {
		if err := validateStdinAuthFlags(cmd); err != nil {
			return nil, err
		}
		password, readErr := readStdinPassword(cmd.Context(), cmd.InOrStdin(), passwordReadTimeout)
		if readErr != nil {
			return nil, readErr
		}
		defer clear(password)
		if cmd.Context().Err() != nil {
			return nil, cmd.Context().Err()
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Signing in for this command only; existing sessions for this user may be closed.")
		session, err = auth.LoginSessionContext(cmd.Context(), apiURL, flagLogin, string(password), flagCAFile)
		if err != nil {
			return nil, err
		}
		rememberSession(cmd, session, instance, true)
		who, verifyErr := session.WhoamiContext(cmd.Context())
		if verifyErr != nil {
			return nil, verifyErr
		}
		// Retain only the matched field; later responses cannot verify its counterpart.
		switch flagLogin {
		case who.Email:
			state.verifiedIdentity = &auth.WhoamiResult{Email: flagLogin}
		case who.Username:
			state.verifiedIdentity = &auth.WhoamiResult{Username: flagLogin}
		default:
			return nil, errors.New("temporary login identity does not match --login; command not run")
		}
	} else if flagCredentialsFile != "" {
		if err := validateLocalCredentialTarget(apiURL); err != nil {
			return nil, err
		}
		if _, set := os.LookupEnv(buildinfo.EnvVar("TOKEN")); set {
			return nil, errors.New("unset the channel TOKEN variable before using --credentials-file")
		}
		login, password, readErr := readLocalCredentials(flagCredentialsFile)
		if readErr != nil {
			return nil, readErr
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Signing in for this command only; existing sessions for this user may be closed.")
		session, err = auth.LoginSessionContext(cmd.Context(), apiURL, login, password, flagCAFile)
	} else {
		session, err = auth.OpenSession(instance, apiURL, flagCAFile)
	}
	if err != nil {
		return nil, err
	}
	rememberSession(cmd, session, instance, flagCredentialsFile != "" || flagPasswordStdin)
	return session, nil
}

const (
	maxStdinPasswordBytes   = 4096
	passwordReadTimeout     = 5 * time.Second
	temporarySignOutTimeout = 5 * time.Second
)

func validateStdinAuthFlags(cmd *cobra.Command) error {
	loginSet := flagLogin != ""
	if flag := cmd.Flag("login"); flag != nil {
		loginSet = loginSet || flag.Changed
	}
	stdinSet := flagPasswordStdin
	if flag := cmd.Flag("password-stdin"); flag != nil {
		stdinSet = stdinSet || flag.Changed
	}
	if !loginSet && !stdinSet {
		return nil
	}
	if !loginSet || !flagPasswordStdin || strings.TrimSpace(flagLogin) == "" {
		return errors.New("--login and --password-stdin must be used together with a non-empty login")
	}
	if flagCredentialsFile != "" || (cmd.Flag("credentials-file") != nil && cmd.Flag("credentials-file").Changed) {
		return errors.New("--login/--password-stdin cannot be combined with --credentials-file")
	}
	if _, set := os.LookupEnv(buildinfo.EnvVar("TOKEN")); set {
		return errors.New("unset the channel TOKEN variable before using --login/--password-stdin")
	}
	if cmd.Name() == "logout" && cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
		return errors.New("--login/--password-stdin already signs out automatically; do not use it with auth logout")
	}
	return nil
}

// Require a complete line: upstream EOF must not submit a partial password.
func readStdinPassword(parent context.Context, input io.Reader, timeout time.Duration) ([]byte, error) {
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return nil, errors.New("--password-stdin requires a pipe or file, not a terminal")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return nil, errors.New("password input canceled or timed out; no login attempted")
	}
	type result struct {
		password []byte
		err      error
	}
	results := make(chan result)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(input, maxStdinPasswordBytes+2)).ReadBytes('\n')
		password := line
		if err != nil {
			err = errors.New("cannot read a complete password line; no login attempted")
		} else {
			password = line[:len(line)-1]
			if len(password) > 0 && password[len(password)-1] == '\r' {
				password = password[:len(password)-1]
			}
			if len(password) == 0 || len(password) > maxStdinPasswordBytes {
				err = errors.New("password must contain 1 to 4096 bytes; no login attempted")
			}
		}
		if err != nil {
			clear(line)
			password = nil
		}
		select {
		case results <- result{password, err}:
		case <-ctx.Done():
			clear(line)
		}
	}()
	select {
	case result := <-results:
		if ctx.Err() != nil {
			clear(result.password)
			return nil, errors.New("password input canceled or timed out; no login attempted")
		}
		return result.password, result.err
	case <-ctx.Done():
		if closer, ok := input.(io.ReadCloser); ok {
			_ = closer.Close()
		}
		return nil, errors.New("password input canceled or timed out; no login attempted")
	}
}

func validateLocalCredentialTarget(apiURL string) error {
	u, err := url.Parse(apiURL)
	if buildinfo.Channel != "local" || err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("--credentials-file requires a local-channel build and a local API origin")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	if (u.Scheme == "https" && (host == "jetscale.internal" || loopback)) || (u.Scheme == "http" && loopback) {
		return nil
	}
	return errors.New("--credentials-file is limited to HTTPS jetscale.internal or a loopback API; credentials were not read")
}

// Read only the two approved keys. Never source shell code or expand variables.
func readLocalCredentials(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", errors.New("cannot open local credentials file")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", "", errors.New("credentials file must be a regular file no larger than 1 MiB")
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(io.LimitReader(file, (1<<20)+1))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || (key != "JETSCALE_DEFAULT_ADMIN_EMAIL" && key != "JETSCALE_DEFAULT_ADMIN_PASSWORD") {
			continue
		}
		if _, exists := values[key]; exists {
			return "", "", errors.New("duplicate admin credential key in local credentials file")
		}
		parsed, parseErr := parseCredentialValue(strings.TrimSpace(value))
		if parseErr != nil {
			return "", "", errors.New("invalid quoted admin credential in local credentials file")
		}
		values[key] = parsed
	}
	if scanner.Err() != nil {
		return "", "", errors.New("cannot parse local credentials file")
	}
	login, password := values["JETSCALE_DEFAULT_ADMIN_EMAIL"], values["JETSCALE_DEFAULT_ADMIN_PASSWORD"]
	if login == "" || password == "" {
		return "", "", errors.New("local credentials file requires non-empty JETSCALE_DEFAULT_ADMIN_EMAIL and JETSCALE_DEFAULT_ADMIN_PASSWORD")
	}
	return login, password, nil
}

func parseCredentialValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] != '\'' && value[0] != '"' {
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		return value, nil
	}
	quote := value[0]
	for i := 1; i < len(value); i++ {
		if quote == '"' && value[i] == '\\' {
			i++
			continue
		}
		if value[i] == quote {
			rest := strings.TrimSpace(value[i+1:])
			if rest != "" && !strings.HasPrefix(rest, "#") {
				break
			}
			if quote == '\'' {
				return value[1:i], nil
			}
			return strconv.Unquote(value[:i+1])
		}
	}
	return "", errors.New("invalid quoted value")
}
