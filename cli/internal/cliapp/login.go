package cliapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/dada-tuda/console/cli/internal/auth"
)

// openBrowser best-effort opens url in the user's default browser. Failure
// is not fatal - the printed verification URL is always the fallback.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}

// Login runs the device authorization grant end to end: request a device
// code, print the code and open the browser, poll until the user finishes
// sign-in, then save the token. It writes progress to out.
func Login(ctx context.Context, cfg Config, out io.Writer) error {
	ep := auth.EndpointsFromIssuer(cfg.Issuer)
	hc := &http.Client{}

	dc, err := auth.StartDeviceAuth(ctx, hc, ep, cfg.ClientID, auth.JoinScopes(auth.RequiredScopes))
	if err != nil {
		return fmt.Errorf("starting login: %w", err)
	}

	fmt.Fprintf(out, "To sign in, visit:\n\n  %s\n\nand enter code: %s\n\n", dc.VerificationURI, dc.UserCode)
	fmt.Fprintf(out, "Opening your browser (code expires in %s)...\n", auth.FormatExpiresIn(dc.ExpiresIn))
	if dc.VerificationURIComplete != "" {
		openBrowser(dc.VerificationURIComplete)
	} else {
		openBrowser(dc.VerificationURI)
	}

	tok, err := auth.PollToken(ctx, hc, ep, cfg.ClientID, dc)
	if err != nil {
		return err
	}

	stored := auth.FromTokenResponse(*tok, cfg.ClientID, cfg.Issuer)
	if err := auth.SaveToken(stored); err != nil {
		return fmt.Errorf("saving login: %w", err)
	}
	fmt.Fprintln(out, "Logged in.")
	return nil
}

// TokenSource returns an apiclient.TokenSource backed by the on-disk cached
// token, transparently refreshing it when expired and re-saving the result.
// It returns an error telling the user to run `ddc login` when no token is
// cached or the refresh itself fails.
func TokenSource(cfg Config) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		tok, ok, err := auth.LoadToken()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("not logged in - run 'ddc login' first")
		}
		if !tok.Expired() {
			return tok.AccessToken, nil
		}
		if tok.RefreshToken == "" {
			return "", fmt.Errorf("session expired - run 'ddc login' again")
		}

		ep := auth.EndpointsFromIssuer(cfg.Issuer)
		hc := &http.Client{}
		refreshed, err := auth.RefreshAccessToken(ep, hc, cfg.ClientID, tok.RefreshToken)
		if err != nil {
			return "", err
		}
		newTok := auth.FromTokenResponse(refreshed, cfg.ClientID, cfg.Issuer)
		if newTok.RefreshToken == "" {
			newTok.RefreshToken = tok.RefreshToken
		}
		if err := auth.SaveToken(newTok); err != nil {
			return "", err
		}
		return newTok.AccessToken, nil
	}
}
