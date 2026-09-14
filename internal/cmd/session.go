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
	"strconv"
	"strings"

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/spf13/cobra"
)

type sessionContextKey struct{}

type commandSessionState struct {
	session   *auth.Session
	instance  string
	ephemeral bool
	fresh     bool
}

// Cleanup belongs around RunE, not PostRunE: Cobra skips post hooks on errors.
func wrapSessionLifecycle(command *cobra.Command) {
	if run := command.RunE; run != nil {
		command.RunE = func(cmd *cobra.Command, args []string) (err error) {
			state := &commandSessionState{}
			cmd.SetContext(context.WithValue(cmd.Context(), sessionContextKey{}, state))
			defer func() {
				if state.session == nil {
					return
				}
				if state.ephemeral {
					if cleanupErr := state.session.SignOut(); cleanupErr != nil {
						err = errors.Join(err, fmt.Errorf("temporary session cleanup failed: %w", cleanupErr))
					} else {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Temporary session signed out; no credentials saved.")
					}
				} else {
					saveErr := state.session.Save(state.instance)
					err = errors.Join(err, saveErr)
					if saveErr != nil && state.fresh {
						if cleanupErr := state.session.SignOut(); cleanupErr != nil {
							err = errors.Join(err, fmt.Errorf("new login cleanup failed: %w", cleanupErr))
						} else {
							_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "New login signed out because credentials could not be saved.")
						}
					}
				}
				state.session.HTTPClient.CloseIdleConnections()
			}()
			return run(cmd, args)
		}
	}
	for _, child := range command.Commands() {
		wrapSessionLifecycle(child)
	}
}

func rememberSession(cmd *cobra.Command, session *auth.Session, instance string, ephemeral bool) {
	state := cmd.Context().Value(sessionContextKey{}).(*commandSessionState)
	state.session, state.instance, state.ephemeral = session, instance, ephemeral
}

func commandSession(cmd *cobra.Command, instance, apiURL string) (*auth.Session, error) {
	state := cmd.Context().Value(sessionContextKey{}).(*commandSessionState)
	if state.session != nil {
		return state.session, nil
	}
	var session *auth.Session
	var err error
	if flagCredentialsFile != "" {
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
		session, err = auth.LoginSession(apiURL, login, password, flagCAFile)
	} else {
		session, err = auth.OpenSession(instance, apiURL, flagCAFile)
	}
	if err != nil {
		return nil, err
	}
	rememberSession(cmd, session, instance, flagCredentialsFile != "")
	return session, nil
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
